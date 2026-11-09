/**
 * 为内置 OCR 模型创建严格的 HiAI NPU 会话，保持 AP 的 NCHW/NTC FP32 契约。
 *
 * 使用 V320 客户端接口明确选择 NPU；MNN 会话禁止 CPU 备援。不支持的算子或驱动错误
 * 抛回 Kotlin，由宿主使用原始 ONNX。此桥的成功执行记录不代替设备硬件性能分析。
 *
 * Creates strict HiAI NPU sessions for bundled OCR models using AP's NCHW/NTC FP32 contract.
 * The V320 client selects NPU explicitly and MNN CPU backup is disabled. Unsupported ops or
 * driver failures return to Kotlin for original ONNX fallback. Execution evidence does not
 * replace hardware profiling on a device.
 */

#include <jni.h>
#include <MNN/Interpreter.hpp>
#include <MNN/Tensor.hpp>
#include <MNNHiAIIO.h>
#include <MNNHiAIPlugin.hpp>
#include <MNNHiAISession.hpp>

#include <cstring>
#include <memory>
#include <stdexcept>
#include <string>
#include <vector>

namespace {

constexpr int kInputElements = 3 * 48 * 320;
constexpr int kMaximumOutputElements = 64 * 1024 * 1024 / 4;

struct OcrSession {
    MNNHiAIDiagnosticsV1 diagnostics{};
    MNNHiAINativeHandleIoContext control{};
    MNNHiAIBackendConfigV1 config{};
    MNN::BackendConfig backend{};
    std::string runtimeDirectory;
    std::unique_ptr<MNN::Interpreter> interpreter;
    MNN::Session* session = nullptr;
    MNN::Tensor* input = nullptr;
    MNN::Tensor* output = nullptr;
    std::unique_ptr<MNN::Tensor> hostInput;
    std::unique_ptr<MNN::Tensor> hostOutput;
};

std::string stringValue(JNIEnv* env, jstring value) {
    if (value == nullptr) throw std::runtime_error("Missing HiAI path");
    const char* bytes = env->GetStringUTFChars(value, nullptr);
    if (bytes == nullptr) throw std::runtime_error("Could not read HiAI path");
    std::string result(bytes);
    env->ReleaseStringUTFChars(value, bytes);
    return result;
}

void fail(JNIEnv* env, const char* message) {
    if (!env->ExceptionCheck()) {
        env->ThrowNew(env->FindClass("java/lang/IllegalStateException"), message);
    }
}

}  // namespace

extern "C" JNIEXPORT jlong JNICALL
Java_com_azurpilot_ghio_ocr_OcrHiaiNative_create(
        JNIEnv* env, jclass, jstring modelPath, jstring libraryDirectory, jint classes) {
    try {
        if (classes <= 0 || 40LL * classes > kMaximumOutputElements) {
            throw std::runtime_error("Invalid HiAI OCR output size");
        }
        std::string error;
        const auto* api = MNN::loadHiAIBackendPlugin(&error);
        if (api == nullptr) throw std::runtime_error(error);
        auto owner = std::make_unique<OcrSession>();
        owner->runtimeDirectory = stringValue(env, libraryDirectory);
        owner->diagnostics.structSize = sizeof(owner->diagnostics);
        owner->diagnostics.version = MNN_HIAI_CONFIG_VERSION;
        owner->control.magic = MNN_HIAI_NATIVE_HANDLE_IO_MAGIC;
        owner->control.version = MNN_HIAI_NATIVE_HANDLE_IO_VERSION;
        owner->control.struct_size = sizeof(owner->control);
        // 不启用 AHardwareBuffer；这里只用控制标志禁用 AUTO 设备选择，明确请求 NPU。
        owner->control.reserved[MNN_HIAI_SESSION_CONTROL_INDEX] = MNN_HIAI_SESSION_FORCE_V320;
        owner->config.structSize = sizeof(owner->config);
        owner->config.version = MNN_HIAI_CONFIG_VERSION;
        owner->config.runtimeLibraryDirectory = owner->runtimeDirectory.c_str();
        owner->config.diagnostics = &owner->diagnostics;
        owner->config.nativeIoContext = &owner->control;
        owner->backend.precision = MNN::BackendConfig::Precision_High;
        owner->backend.sharedContext = &owner->config;
        owner->interpreter.reset(MNN::Interpreter::createFromFile(stringValue(env, modelPath).c_str()));
        if (!owner->interpreter) throw std::runtime_error("Could not read MNN OCR model");
        owner->interpreter->setSessionMode(MNN::Interpreter::Session_Release);
        MNN::ScheduleConfig schedule;
        schedule.type = MNN_FORWARD_USER_0;
        schedule.backupType = MNN_FORWARD_USER_0;
        schedule.numThread = 1;
        schedule.backendConfig = &owner->backend;
        const auto created = MNN::createHiAISession(owner->interpreter.get(), schedule, api);
        if (created.session == nullptr) throw std::runtime_error(created.message);
        owner->session = created.session;
        int backends[2] = {-1, -1};
        if (!owner->interpreter->getSessionInfo(owner->session, MNN::Interpreter::BACKENDS, backends) ||
            backends[0] != MNN_FORWARD_USER_0 || owner->diagnostics.readyCount == 0 ||
            owner->control.reserved[MNN_HIAI_SESSION_STATUS_INDEX] != MNN_HIAI_SESSION_V320_READY) {
            throw std::runtime_error("HiAI did not create a ready NPU-only session");
        }
        const auto& inputs = owner->interpreter->getSessionInputAll(owner->session);
        const auto& outputs = owner->interpreter->getSessionOutputAll(owner->session);
        if (inputs.size() != 1 || outputs.size() != 1) {
            throw std::runtime_error("Unexpected MNN OCR IO count");
        }
        owner->input = inputs.begin()->second;
        owner->output = outputs.begin()->second;
        owner->hostInput = std::make_unique<MNN::Tensor>(owner->input, MNN::Tensor::CAFFE);
        owner->hostOutput = std::make_unique<MNN::Tensor>(owner->output, MNN::Tensor::CAFFE);
        if (owner->hostInput->shape() != std::vector<int>({1, 3, 48, 320}) ||
            owner->hostOutput->shape() != std::vector<int>({1, 40, classes}) ||
            owner->hostInput->getType() != halide_type_of<float>() ||
            owner->hostOutput->getType() != halide_type_of<float>()) {
            throw std::runtime_error("MNN OCR input/output contract changed");
        }
        return reinterpret_cast<jlong>(owner.release());
    } catch (const std::exception& error) {
        fail(env, error.what());
        return 0;
    }
}

extern "C" JNIEXPORT jfloatArray JNICALL
Java_com_azurpilot_ghio_ocr_OcrHiaiNative_run(JNIEnv* env, jclass, jlong handle, jfloatArray values) {
    try {
        auto* owner = reinterpret_cast<OcrSession*>(handle);
        if (owner == nullptr || values == nullptr || env->GetArrayLength(values) != kInputElements) {
            throw std::runtime_error("Invalid HiAI input buffer");
        }
        env->GetFloatArrayRegion(values, 0, kInputElements, owner->hostInput->host<float>());
        if (env->ExceptionCheck()) return nullptr;
        if (!owner->input->copyFromHostTensor(owner->hostInput.get()) ||
            owner->diagnostics.code != MNN::NO_ERROR ||
            owner->interpreter->runSession(owner->session) != MNN::NO_ERROR ||
            owner->diagnostics.code != MNN::NO_ERROR) {
            throw std::runtime_error(owner->diagnostics.message[0] ?
                                     owner->diagnostics.message : "HiAI NPU execution failed");
        }
        if (!owner->output->copyToHostTensor(owner->hostOutput.get()) ||
            owner->diagnostics.code != MNN::NO_ERROR) {
            throw std::runtime_error("HiAI output copy failed");
        }
        const int count = owner->hostOutput->elementSize();
        jfloatArray result = env->NewFloatArray(count);
        if (result != nullptr) env->SetFloatArrayRegion(result, 0, count, owner->hostOutput->host<float>());
        return result;
    } catch (const std::exception& error) {
        fail(env, error.what());
        return nullptr;
    }
}

extern "C" JNIEXPORT void JNICALL
Java_com_azurpilot_ghio_ocr_OcrHiaiNative_close(JNIEnv*, jclass, jlong handle) {
    delete reinterpret_cast<OcrSession*>(handle);
}

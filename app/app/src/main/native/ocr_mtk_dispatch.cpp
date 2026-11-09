// 在钉版 MTK dispatch 的公开 ABI 外修正无填充张量描述，不修改厂商 SDK 或精度门槛。
// 原厂接口保留所有设备、执行和同步行为；真实填充仍按原厂要求处理。
//
// Corrects unpadded tensor requirements around the pinned MTK dispatch public ABI without
// modifying the SDK or accuracy gate. Preserves vendor device, execution, and synchronization
// behavior; actual padded requirements remain unchanged.

#include <dlfcn.h>
#include <limits>
#include <mutex>
#include <vector>
#include "litert/vendors/c/litert_dispatch_api.h"
#include "litert/c/litert_tensor_buffer_requirements.h"
#if defined(__ANDROID__)
#include <android/log.h>
#endif

namespace {
LiteRtDispatchApi vendor_api{};
LiteRtDispatchInterface interface{};
LiteRtStatus load_status = kLiteRtStatusErrorDynamicLoading;
std::once_flag load_once;

struct Runtime {
    decltype(&LiteRtGetTensorBufferRequirementsBufferSize) size;
    decltype(&LiteRtGetTensorBufferRequirementsStrides) strides;
    decltype(&LiteRtGetTensorBufferRequirementsAlignment) alignment;
    decltype(&LiteRtGetNumTensorBufferRequirementsSupportedBufferTypes) count;
    decltype(&LiteRtGetTensorBufferRequirementsSupportedTensorBufferType) type;
    decltype(&LiteRtCreateTensorBufferRequirementsWithAlignment) create;
    decltype(&LiteRtDestroyTensorBufferRequirements) destroy;
} runtime{};

void Load() {
    // 独立名称防止再次打开本包装库；句柄随 OCR 工作进程释放。
    void* vendor = dlopen("libLiteRtDispatch_MediaTek_Vendor.so", RTLD_NOW | RTLD_LOCAL);
    void* core = dlopen("libLiteRt.so", RTLD_NOW | RTLD_LOCAL);
    if (!vendor || !core) return;
    auto get_api = reinterpret_cast<decltype(&LiteRtDispatchGetApi)>(
            dlsym(vendor, "LiteRtDispatchGetApi"));
    if (!get_api) return;
    load_status = get_api(&vendor_api);
    if (load_status != kLiteRtStatusOk) return;
    if (vendor_api.version.major != LITERT_API_VERSION_MAJOR ||
        vendor_api.version.minor != LITERT_API_VERSION_MINOR ||
        vendor_api.version.patch != LITERT_API_VERSION_PATCH) {
        load_status = kLiteRtStatusErrorWrongVersion;
        return;
    }
#define LOAD_RUNTIME(field, name) \
    runtime.field = reinterpret_cast<decltype(runtime.field)>(dlsym(core, #name)); \
    if (!runtime.field) { load_status = kLiteRtStatusErrorDynamicLoading; return; }
    LOAD_RUNTIME(size, LiteRtGetTensorBufferRequirementsBufferSize)
    LOAD_RUNTIME(strides, LiteRtGetTensorBufferRequirementsStrides)
    LOAD_RUNTIME(alignment, LiteRtGetTensorBufferRequirementsAlignment)
    LOAD_RUNTIME(count, LiteRtGetNumTensorBufferRequirementsSupportedBufferTypes)
    LOAD_RUNTIME(type, LiteRtGetTensorBufferRequirementsSupportedTensorBufferType)
    LOAD_RUNTIME(create, LiteRtCreateTensorBufferRequirementsWithAlignment)
    LOAD_RUNTIME(destroy, LiteRtDestroyTensorBufferRequirements)
#undef LOAD_RUNTIME
    if (!vendor_api.interface || !vendor_api.interface->get_input_requirements ||
        !vendor_api.interface->get_output_requirements) {
        load_status = kLiteRtStatusErrorInvalidArgument;
        return;
    }
    interface = *vendor_api.interface;
}

LiteRtStatus Requirements(LiteRtDispatchGetInputRequirementsT original,
        LiteRtDispatchInvocationContext context, int index,
        const LiteRtRankedTensorType* type, LiteRtTensorBufferRequirements* result) {
    if (!type || !result) return kLiteRtStatusErrorInvalidArgument;
    LiteRtStatus status = original(context, index, type, result);
    if (status != kLiteRtStatusOk) return status;
    // 只处理已知 FP32 静态布局；类型或尺寸不明时保留原厂要求。
    if (type->element_type != kLiteRtElementTypeFloat32 || type->layout.rank < 1 ||
        type->layout.rank > 4 || type->layout.has_strides) return kLiteRtStatusOk;
    size_t packed = sizeof(float);
    for (unsigned i = 0; i < type->layout.rank; ++i) {
        const int32_t dimension = type->layout.dimensions[i];
        if (dimension <= 0 || packed > std::numeric_limits<size_t>::max() / dimension)
            return kLiteRtStatusOk;
        packed *= dimension;
    }
    size_t size = 0;
    int num_strides = 0;
    const uint32_t* strides = nullptr;
    if (runtime.size(*result, &size) != kLiteRtStatusOk || size != packed ||
        runtime.strides(*result, &num_strides, &strides) != kLiteRtStatusOk ||
        num_strides == 0) return kLiteRtStatusOk;
    // 钉版厂商实现把填充维度写成 stride；无填充时无需声明 stride。
    // 上游 2.2 同样清除无填充描述；此处同时检查大小和全部已报告维度。
    if (num_strides != type->layout.rank || !strides || strides[0] != 1)
        return kLiteRtStatusOk;
    for (int i = 1; i < num_strides; ++i) {
        if (strides[i] != static_cast<uint32_t>(type->layout.dimensions[i - 1]))
            return kLiteRtStatusOk;
    }
    int count = 0;
    size_t alignment = 0;
    if (runtime.count(*result, &count) != kLiteRtStatusOk || count <= 0 || count > 16 ||
        runtime.alignment(*result, &alignment) != kLiteRtStatusOk) return kLiteRtStatusOk;
    std::vector<LiteRtTensorBufferType> types(count);
    for (int i = 0; i < count; ++i) {
        if (runtime.type(*result, i, &types[i]) != kLiteRtStatusOk) return kLiteRtStatusOk;
    }
    LiteRtTensorBufferRequirements replacement = nullptr;
    status = runtime.create(count, types.data(), size, 0, nullptr, alignment, &replacement);
    if (status != kLiteRtStatusOk) {
        runtime.destroy(*result);
        *result = nullptr;
        return status;
    }
    runtime.destroy(*result);
    *result = replacement;
#if defined(__ANDROID__)
    __android_log_print(ANDROID_LOG_INFO, "OcrMtkDispatch",
                        "Cleared unpadded FP32 strides: rank=%u bytes=%zu index=%d",
                        type->layout.rank, size, index);
#endif
    return kLiteRtStatusOk;
}

LiteRtStatus Input(LiteRtDispatchInvocationContext context, int index,
        const LiteRtRankedTensorType* type, LiteRtTensorBufferRequirements* result) {
    return Requirements(vendor_api.interface->get_input_requirements, context, index, type, result);
}

LiteRtStatus Output(LiteRtDispatchInvocationContext context, int index,
        const LiteRtRankedTensorType* type, LiteRtTensorBufferRequirements* result) {
    return Requirements(vendor_api.interface->get_output_requirements, context, index, type, result);
}
}  // namespace

// 返回与钉版完全匹配的接口；只替换两个公开的缓冲区要求回调。
// Returns the exact pinned interface, replacing only the public buffer-requirement callbacks.
extern "C" LITERT_CAPI_EXPORT LiteRtStatus LiteRtDispatchGetApi(LiteRtDispatchApi* api) {
    if (!api) return kLiteRtStatusErrorInvalidArgument;
    std::call_once(load_once, [] {
        Load();
        if (load_status == kLiteRtStatusOk) {
            interface.get_input_requirements = Input;
            interface.get_output_requirements = Output;
        }
    });
    if (load_status != kLiteRtStatusOk) return load_status;
    *api = vendor_api;
    api->interface = &interface;
    return kLiteRtStatusOk;
}

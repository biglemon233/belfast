// 用公开 C ABI 模拟厂商和核心库，验证生产包装库的布局、所有权和原接口转发。
//
// Mocks vendor and core libraries through public C ABI to test production-wrapper layouts,
// ownership, and forwarding.
#include <cstdlib>
#include <cstring>
#include <vector>
#include <thread>
#include "litert/vendors/c/litert_dispatch_api.h"
#include "litert/c/litert_tensor_buffer_requirements.h"

#if defined(FIXTURE_CORE)
struct LiteRtTensorBufferRequirementsT {
    size_t size, alignment;
    std::vector<uint32_t> strides;
    std::vector<LiteRtTensorBufferType> types;
};
static int live = 0;
extern "C" int FixtureLiveRequirements() { return live; }
LiteRtStatus LiteRtCreateTensorBufferRequirementsWithAlignment(int count,
        const LiteRtTensorBufferType* types, size_t size, int num_strides,
        const uint32_t* strides, size_t alignment, LiteRtTensorBufferRequirements* result) {
    if (num_strides == 0 && std::getenv("FIXTURE_ALLOCATION_FAILURE"))
        return kLiteRtStatusErrorMemoryAllocationFailure;
    *result = new LiteRtTensorBufferRequirementsT{size, alignment, {}, {types, types + count}};
    if (num_strides) (*result)->strides.assign(strides, strides + num_strides);
    ++live;
    return kLiteRtStatusOk;
}
LiteRtStatus LiteRtGetTensorBufferRequirementsBufferSize(LiteRtTensorBufferRequirements r, size_t* n) {
    *n = r->size; return kLiteRtStatusOk;
}
LiteRtStatus LiteRtGetTensorBufferRequirementsAlignment(LiteRtTensorBufferRequirements r, size_t* n) {
    *n = r->alignment; return kLiteRtStatusOk;
}
LiteRtStatus LiteRtGetTensorBufferRequirementsStrides(LiteRtTensorBufferRequirements r,
        int* n, const uint32_t** s) {
    *n = r->strides.size(); *s = r->strides.data(); return kLiteRtStatusOk;
}
LiteRtStatus LiteRtGetNumTensorBufferRequirementsSupportedBufferTypes(LiteRtTensorBufferRequirements r, int* n) {
    *n = r->types.size(); return kLiteRtStatusOk;
}
LiteRtStatus LiteRtGetTensorBufferRequirementsSupportedTensorBufferType(LiteRtTensorBufferRequirements r,
        int index, LiteRtTensorBufferType* t) {
    *t = r->types.at(index); return kLiteRtStatusOk;
}
void LiteRtDestroyTensorBufferRequirements(LiteRtTensorBufferRequirements r) {
    if (r) { delete r; --live; }
}
#elif defined(FIXTURE_VENDOR)
static LiteRtStatus Requirements(LiteRtDispatchInvocationContext, int index,
        const LiteRtRankedTensorType* t, LiteRtTensorBufferRequirements* r) {
    if (t->layout.rank > 4) return kLiteRtStatusErrorRuntimeFailure;
    std::vector<uint32_t> strides(t->layout.rank, 1);
    size_t bytes = sizeof(float);
    for (unsigned i = 0; i < t->layout.rank; ++i) {
        bytes *= t->layout.dimensions[i] > 0 ? t->layout.dimensions[i] : 1;
        if (i) strides[i] = t->layout.dimensions[i - 1];
    }
    if (index == 1) bytes += 64;
    if (index == 2 && strides.size() > 1) strides[1] += 1;
    LiteRtTensorBufferType types[] = {kLiteRtTensorBufferTypeAhwb, kLiteRtTensorBufferTypeDmaBuf};
    return LiteRtCreateTensorBufferRequirementsWithAlignment(2, types, bytes,
            strides.size(), strides.data(), 128, r);
}
static LiteRtStatus Invoke(LiteRtDispatchInvocationContext context) {
    return reinterpret_cast<uintptr_t>(context) == 123 ? kLiteRtStatusOk : kLiteRtStatusErrorInvalidArgument;
}
extern "C" LITERT_CAPI_EXPORT LiteRtStatus LiteRtDispatchGetApi(LiteRtDispatchApi* api) {
    static LiteRtDispatchInterface interface{};
    interface.get_input_requirements = Requirements;
    interface.get_output_requirements = Requirements;
    interface.invoke = Invoke;
    *api = {{LITERT_API_VERSION_MAJOR, LITERT_API_VERSION_MINOR, LITERT_API_VERSION_PATCH},
            &interface, nullptr, nullptr};
    if (std::getenv("FIXTURE_BAD_VERSION")) ++api->version.minor;
    return kLiteRtStatusOk;
}
#else
#include <dlfcn.h>
#include <iostream>
extern "C" int FixtureLiveRequirements();
static void Check(bool condition) { if (!condition) std::abort(); }
int main(int argc, char** argv) {
    void* library = dlopen("libWrapper.so", RTLD_NOW | RTLD_LOCAL);
    Check(library);
    auto get_api = reinterpret_cast<decltype(&LiteRtDispatchGetApi)>(dlsym(library, "LiteRtDispatchGetApi"));
    Check(get_api);
    LiteRtDispatchApi api{};
    if (argc > 1) {
        Check(get_api(&api) == (std::strcmp(argv[1], "bad_version") == 0 ?
              kLiteRtStatusErrorWrongVersion : kLiteRtStatusErrorDynamicLoading));
        return 0;
    }
    Check(get_api(nullptr) == kLiteRtStatusErrorInvalidArgument);
    Check(get_api(&api) == kLiteRtStatusOk);
    std::vector<std::thread> threads;
    for (int i = 0; i < 8; ++i) threads.emplace_back([&] {
        LiteRtDispatchApi copy{}; Check(get_api(&copy) == kLiteRtStatusOk && copy.interface == api.interface);
    });
    for (auto& thread : threads) thread.join();
    Check(api.interface->invoke(reinterpret_cast<LiteRtDispatchInvocationContext>(123)) == kLiteRtStatusOk);
    for (auto callback : {api.interface->get_input_requirements, api.interface->get_output_requirements}) {
        for (unsigned rank = 1; rank <= 4; ++rank) {
            LiteRtRankedTensorType type{};
            type.element_type = kLiteRtElementTypeFloat32;
            type.layout.rank = rank;
            for (unsigned i = 0; i < rank; ++i) type.layout.dimensions[i] = i + 2;
            for (int mode = 0; mode <= 2; ++mode) {
                LiteRtTensorBufferRequirements r = nullptr;
                Check(callback(nullptr, mode, &type, &r) == kLiteRtStatusOk);
                int n; const uint32_t* strides; size_t alignment;
                LiteRtGetTensorBufferRequirementsStrides(r, &n, &strides);
                Check(n == (mode == 0 || (mode == 2 && rank == 1) ? 0 : static_cast<int>(rank)));
                LiteRtGetTensorBufferRequirementsAlignment(r, &alignment); Check(alignment == 128);
                LiteRtGetNumTensorBufferRequirementsSupportedBufferTypes(r, &n); Check(n == 2);
                LiteRtDestroyTensorBufferRequirements(r);
            }
            for (int mode = 0; mode < 3; ++mode) {
                auto unsupported = type;
                if (mode == 0) unsupported.element_type = kLiteRtElementTypeFloat16;
                if (mode == 1) unsupported.layout.has_strides = true;
                if (mode == 2) unsupported.layout.dimensions[0] = -1;
                LiteRtTensorBufferRequirements r = nullptr;
                Check(callback(nullptr, 0, &unsupported, &r) == kLiteRtStatusOk);
                int n; const uint32_t* strides;
                LiteRtGetTensorBufferRequirementsStrides(r, &n, &strides); Check(n == static_cast<int>(rank));
                LiteRtDestroyTensorBufferRequirements(r);
            }
            setenv("FIXTURE_ALLOCATION_FAILURE", "1", 1);
            LiteRtTensorBufferRequirements r = nullptr;
            Check(callback(nullptr, 0, &type, &r) == kLiteRtStatusErrorMemoryAllocationFailure && !r);
            unsetenv("FIXTURE_ALLOCATION_FAILURE");
            Check(FixtureLiveRequirements() == 0);
        }
        LiteRtRankedTensorType type{}; type.layout.rank = 5;
        LiteRtTensorBufferRequirements r = nullptr;
        Check(callback(nullptr, 0, &type, &r) == kLiteRtStatusErrorRuntimeFailure && !r);
        Check(callback(nullptr, 0, nullptr, &r) == kLiteRtStatusErrorInvalidArgument);
    }
    std::cout << "MTK dispatch wrapper: layouts, padding, forwarding, version, and ownership checks passed\n";
}
#endif

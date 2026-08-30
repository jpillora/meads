set(CMAKE_SYSTEM_NAME WASI)
set(CMAKE_SYSTEM_PROCESSOR wasm32)

set(CMAKE_C_COMPILER "${CMAKE_CURRENT_LIST_DIR}/zig-cc")
set(CMAKE_AR "${CMAKE_CURRENT_LIST_DIR}/zig-ar")
set(CMAKE_RANLIB "${CMAKE_CURRENT_LIST_DIR}/zig-ranlib")

set(CMAKE_C_COMPILER_TARGET wasm32-wasi)
set(CMAKE_TRY_COMPILE_TARGET_TYPE STATIC_LIBRARY)

# libgit2's local object/ref path does not need sockets or mmap. WASI Preview 1
# has neither, and explicitly selecting the portable code keeps accidental
# network dependencies out of the embedded module.
set(CMAKE_C_FLAGS_INIT "-DNO_MMAP -D_WASI_EMULATED_GETPID -D_WASI_EMULATED_SIGNAL -D_WASI_EMULATED_PROCESS_CLOCKS")

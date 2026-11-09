/* Static aarch64 exec shim for Spike A: run a path with a controlled argv[0].

   Multi-call binaries (busybox) pick their applet from argv[0]; a file named
   libbusybox.so cannot be renamed per-applet inside nativeLibraryDir, so the
   app exec's this shim instead:

       libspike_shim.so <argv0> <path> [args...]

   which execve()s <path> with argv = [<argv0>, args...].
*/
#include <stdio.h>
#include <unistd.h>

extern char **environ;

int main(int argc, char **argv) {
    if (argc < 3) {
        fprintf(stderr, "usage: %s <argv0> <path> [args...]\n", argv[0]);
        return 2;
    }
    const char *path = argv[2];
    char **out = argv + 1; /* out[0] = argv[1] (forced argv[0]) */
    for (int i = 1; i <= argc - 2; i++) {
        out[i] = argv[i + 2]; /* shift real args left, terminate with the NULL */
    }
    execve(path, out, environ);
    perror("execve");
    return 127;
}

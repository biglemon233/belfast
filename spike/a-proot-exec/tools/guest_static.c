#include <stdio.h>
#include <string.h>
#include <sys/utsname.h>
#include <unistd.h>

int main(void) {
    struct utsname u;
    memset(&u, 0, sizeof(u));
    uname(&u);
    printf("STATIC_GUEST_OK pid=%d sysname=%s machine=%s release=%s\n",
           (int)getpid(), u.sysname, u.machine, u.release);
    fflush(stdout);
    return 0;
}

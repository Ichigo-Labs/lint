#include <cstdlib>
#include <cstring>

void fill(const char *src) {
    char *dst = (char *)malloc(128);
    strcpy(dst, src);
}

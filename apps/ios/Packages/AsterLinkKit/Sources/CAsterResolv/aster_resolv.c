#include "aster_resolv.h"

#include <arpa/inet.h>
#include <netinet/in.h>
#include <resolv.h>
#include <string.h>
#include <sys/socket.h>

int aster_dns_servers(char *out, size_t out_size) {
    if (out == NULL || out_size == 0) {
        return -1;
    }
    out[0] = '\0';
    struct __res_state state;
    memset(&state, 0, sizeof(state));
    if (res_ninit(&state) != 0) {
        return -1;
    }
    union res_sockaddr_union servers[MAXNS];
    int count = res_getservers(&state, servers, MAXNS);
    int written = 0;
    size_t used = 0;
    for (int i = 0; i < count; i++) {
        char text[INET6_ADDRSTRLEN];
        const char *ok = NULL;
        if (servers[i].sin.sin_family == AF_INET) {
            ok = inet_ntop(AF_INET, &servers[i].sin.sin_addr, text, sizeof(text));
        } else if (servers[i].sin6.sin6_family == AF_INET6) {
            ok = inet_ntop(AF_INET6, &servers[i].sin6.sin6_addr, text, sizeof(text));
        }
        if (ok == NULL) {
            continue;
        }
        size_t length = strlen(text);
        if (used + length + 2 > out_size) {
            break;
        }
        memcpy(out + used, text, length);
        used += length;
        out[used++] = '\n';
        out[used] = '\0';
        written++;
    }
    res_ndestroy(&state);
    return written;
}

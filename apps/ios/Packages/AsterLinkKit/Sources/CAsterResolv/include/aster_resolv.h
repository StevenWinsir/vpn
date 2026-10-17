#ifndef ASTER_RESOLV_H
#define ASTER_RESOLV_H

#include <stddef.h>

/// Writes the system resolver's name servers (res_getservers, numeric, IPv4 and IPv6) into
/// out as a newline-separated list and returns how many were written, or -1 when the resolver
/// configuration could not be read. Used for the Core's host network report
/// (handoff_app_ios.md 7): nothing here is secret and nothing is sent anywhere.
int aster_dns_servers(char *out, size_t out_size);

#endif

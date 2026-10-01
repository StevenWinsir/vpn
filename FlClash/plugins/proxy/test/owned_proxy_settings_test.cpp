#include "../windows/owned_proxy_settings.h"
#include <cassert>
#include <iostream>

int main() {
  using proxy::ProxySettingsSnapshot;
  using proxy::RestoreOwnedProxySettings;
  const ProxySettingsSnapshot original{5, L"old.example:8888", L"original"};
  const ProxySettingsSnapshot applied{3, L"127.0.0.1:7890", L"localhost"};
  auto restored = RestoreOwnedProxySettings(original, applied, applied);
  assert(restored && restored->flags == original.flags && restored->server == original.server && restored->bypass == original.bypass);
  auto changed = applied;
  changed.server = L"another-app:9999";
  assert(!RestoreOwnedProxySettings(original, applied, changed));
  changed = applied; changed.bypass = L"user-bypass";
  restored = RestoreOwnedProxySettings(original, applied, changed);
  assert(restored && restored->bypass == L"user-bypass" && restored->server == original.server);
  changed = applied; changed.flags = 1;
  restored = RestoreOwnedProxySettings(original, applied, changed);
  assert(restored && restored->flags == 1 && restored->server == original.server);
  changed = original; changed.flags = applied.flags;
  restored = RestoreOwnedProxySettings(original, applied, changed, true);
  assert(restored && restored->flags == original.flags);
  std::cout << "5 Windows ownership-policy cases passed (portable C++; not WinInet integration)\n";
}

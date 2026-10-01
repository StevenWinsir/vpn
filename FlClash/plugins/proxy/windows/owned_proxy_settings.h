#ifndef FLCLASH_OWNED_PROXY_SETTINGS_H_
#define FLCLASH_OWNED_PROXY_SETTINGS_H_

#include <cstdint>
#include <optional>
#include <string>

namespace proxy {
struct ProxySettingsSnapshot {
  std::uint32_t flags = 0;
  std::wstring server;
  std::wstring bypass;
};

inline std::optional<ProxySettingsSnapshot> RestoreOwnedProxySettings(
    const ProxySettingsSnapshot& original,
    const ProxySettingsSnapshot& applied,
    const ProxySettingsSnapshot& current,
    bool incomplete = false) {
  const bool partial = incomplete && current.server == original.server &&
      current.flags == applied.flags &&
      (current.bypass == original.bypass || current.bypass == applied.bypass);
  if (current.server != applied.server && !partial) return std::nullopt;
  auto restored = current;
  restored.server = original.server;
  if (current.flags == applied.flags) restored.flags = original.flags;
  if (current.bypass == applied.bypass) restored.bypass = original.bypass;
  return restored;
}
}  // namespace proxy
#endif

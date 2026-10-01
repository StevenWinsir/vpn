#include "proxy_plugin.h"
#include "owned_proxy_settings.h"

// This must be included before many other Windows headers.
#include <windows.h>

#include <WinInet.h>
#include <Ras.h>
#include <RasError.h>
#include <algorithm>
#include <string>
#include <vector>
#include <map>

#pragma comment(lib, "wininet")
#pragma comment(lib, "Rasapi32")

#include <flutter/method_channel.h>
#include <flutter/plugin_registrar_windows.h>
#include <flutter/standard_method_codec.h>

#include <memory>

namespace
{

constexpr int kMinProxyPort = 1;
constexpr int kMaxProxyPort = 65535;

std::wstring Utf8ToWide(const std::string& value)
{
  if (value.empty())
  {
    return {};
  }
  const int size = MultiByteToWideChar(
      CP_UTF8, 0, value.c_str(), static_cast<int>(value.size()), nullptr, 0);
  if (size <= 0)
  {
    return std::wstring(value.begin(), value.end());
  }
  std::wstring result(size, L'\0');
  MultiByteToWideChar(
      CP_UTF8, 0, value.c_str(), static_cast<int>(value.size()),
      result.data(), size);
  return result;
}

std::wstring BuildBypassList(const flutter::EncodableList& bypassDomain)
{
  std::wstring bypassList;
  for (const auto& domain : bypassDomain)
  {
    const auto& value = std::get<std::string>(domain);
    if (!bypassList.empty())
    {
      bypassList += L";";
    }
    bypassList += Utf8ToWide(value);
  }
  return bypassList;
}

bool IsStringList(const flutter::EncodableList& values)
{
  return std::all_of(
      values.begin(), values.end(), [](const auto& value)
      {
        return std::holds_alternative<std::string>(value);
      });
}

bool SetOptionsForConnection(
    INTERNET_PER_CONN_OPTION_LIST& list,
    LPTSTR connection)
{
  list.pszConnection = connection;
  return InternetSetOption(
      nullptr,
      INTERNET_OPTION_PER_CONNECTION_OPTION,
      &list,
      sizeof(list)) != FALSE;
}

std::optional<std::vector<std::wstring>> Connections()
{
  std::vector<std::wstring> connections{L""};
  DWORD size = 0;
  DWORD count = 0;
  auto ret = RasEnumEntries(nullptr, nullptr, nullptr, &size, &count);
  if (ret == ERROR_BUFFER_TOO_SMALL && count > 0)
  {
    std::vector<RASENTRYNAME> entries(count);
    for (auto& entry : entries)
    {
      entry.dwSize = sizeof(RASENTRYNAME);
    }
    ret = RasEnumEntries(nullptr, nullptr, entries.data(), &size, &count);
    if (ret == ERROR_SUCCESS)
    {
      for (DWORD i = 0; i < count; i++)
      {
        connections.emplace_back(entries[i].szEntryName);
      }
    }
    else
    {
      return std::nullopt;
    }
  }
  else if (ret != ERROR_SUCCESS)
  {
    return std::nullopt;
  }

  return connections;
}

using Snapshot = proxy::ProxySettingsSnapshot;
struct OwnedSnapshot { Snapshot original; Snapshot applied; bool complete = false; };
std::map<std::wstring, OwnedSnapshot> owned_settings;

std::optional<Snapshot> ReadSnapshot(const std::wstring& connection)
{
  std::vector<INTERNET_PER_CONN_OPTION> options(3);
  options[0].dwOption = INTERNET_PER_CONN_FLAGS;
  options[1].dwOption = INTERNET_PER_CONN_PROXY_SERVER;
  options[2].dwOption = INTERNET_PER_CONN_PROXY_BYPASS;
  INTERNET_PER_CONN_OPTION_LIST list = {};
  list.dwSize = sizeof(list);
  list.pszConnection = connection.empty() ? nullptr : const_cast<wchar_t*>(connection.c_str());
  list.dwOptionCount = static_cast<DWORD>(options.size());
  list.pOptions = options.data();
  DWORD size = sizeof(list);
  const bool success = InternetQueryOption(nullptr, INTERNET_OPTION_PER_CONNECTION_OPTION, &list, &size) != FALSE;
  Snapshot snapshot;
  if (success) {
    snapshot.flags = options[0].Value.dwValue;
    if (options[1].Value.pszValue) snapshot.server = options[1].Value.pszValue;
    if (options[2].Value.pszValue) snapshot.bypass = options[2].Value.pszValue;
  }
  if (options[1].Value.pszValue) GlobalFree(options[1].Value.pszValue);
  if (options[2].Value.pszValue) GlobalFree(options[2].Value.pszValue);
  return success ? std::optional<Snapshot>(snapshot) : std::nullopt;
}

bool WriteSnapshot(const std::wstring& connection, const Snapshot& snapshot)
{
  std::vector<INTERNET_PER_CONN_OPTION> options(3);
  options[0].dwOption = INTERNET_PER_CONN_FLAGS;
  options[0].Value.dwValue = snapshot.flags;
  options[1].dwOption = INTERNET_PER_CONN_PROXY_SERVER;
  options[1].Value.pszValue = const_cast<wchar_t*>(snapshot.server.c_str());
  options[2].dwOption = INTERNET_PER_CONN_PROXY_BYPASS;
  options[2].Value.pszValue = const_cast<wchar_t*>(snapshot.bypass.c_str());
  INTERNET_PER_CONN_OPTION_LIST list = {};
  list.dwSize = sizeof(list);
  list.dwOptionCount = static_cast<DWORD>(options.size());
  list.pOptions = options.data();
  return SetOptionsForConnection(list, connection.empty() ? nullptr : const_cast<wchar_t*>(connection.c_str()));
}

bool NotifySettingsChanged()
{
  const bool changed = InternetSetOption(
      nullptr, INTERNET_OPTION_SETTINGS_CHANGED, nullptr, 0) != FALSE;
  const bool refreshed = InternetSetOption(
      nullptr, INTERNET_OPTION_REFRESH, nullptr, 0) != FALSE;
  return changed && refreshed;
}

bool startProxy(const int port, const flutter::EncodableList& bypassDomain)
{
  const auto connections = Connections();
  if (!connections) return false;
  const Snapshot applied{PROXY_TYPE_DIRECT | PROXY_TYPE_PROXY,
      Utf8ToWide("127.0.0.1:" + std::to_string(port)), BuildBypassList(bypassDomain)};
  std::map<std::wstring, Snapshot> before;
  for (const auto& connection : *connections) {
    const auto current = ReadSnapshot(connection);
    if (!current) return false;
    const auto existing = owned_settings.find(connection);
    if (existing != owned_settings.end() && current->server != existing->second.applied.server) return false;
    before.emplace(connection, *current);
  }
  for (const auto& entry : before) {
    auto original = entry.second;
    const auto existing = owned_settings.find(entry.first);
    if (existing != owned_settings.end()) original = existing->second.original;
    owned_settings[entry.first] = OwnedSnapshot{original, applied, false};
    if (!WriteSnapshot(entry.first, applied)) return false;
    const auto actual = ReadSnapshot(entry.first);
    if (!actual || actual->flags != applied.flags || actual->server != applied.server || actual->bypass != applied.bypass) return false;
    owned_settings[entry.first].complete = true;
  }
  return NotifySettingsChanged();
}

bool stopProxy()
{
  if (owned_settings.empty()) return true;
  bool success = true;
  bool changed = false;
  for (auto iterator = owned_settings.begin(); iterator != owned_settings.end();) {
    const auto current = ReadSnapshot(iterator->first);
    if (!current) { success = false; ++iterator; continue; }
    const auto& owner = iterator->second;
    const auto restored = proxy::RestoreOwnedProxySettings(owner.original, owner.applied, *current, !owner.complete);
    if (restored) {
      if (!WriteSnapshot(iterator->first, *restored)) { success = false; ++iterator; continue; }
      changed = true;
    }
    iterator = owned_settings.erase(iterator);
  }
  return (changed ? NotifySettingsChanged() : true) && success;
}

}  // namespace

namespace proxy
{

  // static
  void ProxyPlugin::RegisterWithRegistrar(
      flutter::PluginRegistrarWindows *registrar)
  {
    auto channel =
        std::make_unique<flutter::MethodChannel<flutter::EncodableValue>>(
            registrar->messenger(), "proxy",
            &flutter::StandardMethodCodec::GetInstance());

    auto plugin = std::make_unique<ProxyPlugin>(registrar);

    channel->SetMethodCallHandler(
        [plugin_pointer = plugin.get()](const auto &call, auto result)
        {
          plugin_pointer->HandleMethodCall(call, std::move(result));
        });

    registrar->AddPlugin(std::move(plugin));
  }

  ProxyPlugin::ProxyPlugin(flutter::PluginRegistrarWindows* registrar)
      : registrar_(registrar)
  {
    window_proc_id_ = registrar_->RegisterTopLevelWindowProcDelegate(
        [this](HWND window, UINT message, WPARAM wparam, LPARAM lparam)
        {
          return HandleWindowProc(window, message, wparam, lparam);
        });
  }

  ProxyPlugin::~ProxyPlugin()
  {
    if (registrar_ != nullptr)
    {
      registrar_->UnregisterTopLevelWindowProcDelegate(window_proc_id_);
    }
  }

  bool ProxyPlugin::IsSessionEnding(UINT message, WPARAM wparam)
  {
    return message == WM_ENDSESSION && wparam != FALSE;
  }

  // Shutting Windows down kills the process without running the Dart exit path,
  // so the setting survives into a boot with nothing listening behind it.
  std::optional<LRESULT> ProxyPlugin::HandleWindowProc(
      HWND window, UINT message, WPARAM wparam, LPARAM lparam)
  {
    if (proxy_applied_ && IsSessionEnding(message, wparam))
    {
      proxy_applied_ = !stopProxy();
    }
    return std::nullopt;
  }

  void ProxyPlugin::HandleMethodCall(
      const flutter::MethodCall<flutter::EncodableValue> &method_call,
      std::unique_ptr<flutter::MethodResult<flutter::EncodableValue>> result)
  {
    if (method_call.method_name() == "StopProxy")
    {
      const bool stopped = stopProxy();
      proxy_applied_ = proxy_applied_ && !stopped;
      result->Success(stopped);
    }
    else if (method_call.method_name() == "StartProxy")
    {
      auto *arguments = std::get_if<flutter::EncodableMap>(method_call.arguments());
      if (arguments == nullptr)
      {
        result->Error("bad_args", "StartProxy requires argument map");
        return;
      }
      auto portIt = arguments->find(flutter::EncodableValue("port"));
      auto bypassDomainIt = arguments->find(flutter::EncodableValue("bypassDomain"));
      if (portIt == arguments->end() || bypassDomainIt == arguments->end())
      {
        result->Error("bad_args", "StartProxy requires port and bypassDomain");
        return;
      }
      auto *port = std::get_if<int>(&portIt->second);
      auto *bypassDomain = std::get_if<flutter::EncodableList>(&bypassDomainIt->second);
      if (port == nullptr || bypassDomain == nullptr)
      {
        result->Error("bad_args", "StartProxy argument types are invalid");
        return;
      }
      if (*port < kMinProxyPort || *port > kMaxProxyPort)
      {
        result->Error("bad_args", "StartProxy port must be between 1 and 65535");
        return;
      }
      if (!IsStringList(*bypassDomain))
      {
        result->Error(
            "bad_args", "StartProxy bypassDomain must contain only strings");
        return;
      }
      // A start that reports failure can still have written the setting.
      proxy_applied_ = true;
      result->Success(startProxy(*port, *bypassDomain));
    }
    else
    {
      result->NotImplemented();
    }
  }
} // namespace proxy

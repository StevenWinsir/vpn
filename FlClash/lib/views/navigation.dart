import 'package:fl_clash/common/app_ports.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:fl_clash/models/models.dart';
import 'package:fl_clash/pages/managed_account.dart';
import 'package:fl_clash/views/managed_preferences.dart';
import 'package:fl_clash/views/managed_proxies.dart';
import 'package:material_ui/material_ui.dart';

class Navigation implements NavigationPort {
  static Navigation? _instance;

  @override
  List<NavigationItem> getItems({
    bool openLogs = false,
    bool hasProxies = false,
  }) {
    return [
      NavigationItem(
        keep: false,
        icon: const Icon(Icons.space_dashboard),
        label: PageLabel.dashboard,
        builder: (_) => const ManagedAccountPanel(
          key: GlobalObjectKey(PageLabel.dashboard),
        ),
      ),
      NavigationItem(
        icon: const Icon(Icons.article),
        label: PageLabel.proxies,
        builder: (_) =>
            const ManagedProxiesView(key: GlobalObjectKey(PageLabel.proxies)),
        modes: hasProxies
            ? [NavigationItemMode.mobile, NavigationItemMode.desktop]
            : [],
      ),
      NavigationItem(
        icon: const Icon(Icons.construction),
        label: PageLabel.tools,
        builder: (_) =>
            const ManagedPreferencesView(key: GlobalObjectKey(PageLabel.tools)),
        modes: [NavigationItemMode.desktop, NavigationItemMode.mobile],
      ),
    ];
  }

  Navigation._internal();

  factory Navigation() {
    _instance ??= Navigation._internal();
    return _instance!;
  }
}

final navigation = Navigation();

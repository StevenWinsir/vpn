import 'package:fl_clash/enum/enum.dart';

void requireLocalConfiguration() {
  throw StateError('managed_configuration_only');
}

PageLabel normalizeManagedPageLabel(PageLabel label) => switch (label) {
  PageLabel.dashboard || PageLabel.proxies || PageLabel.tools => label,
  _ => PageLabel.dashboard,
};

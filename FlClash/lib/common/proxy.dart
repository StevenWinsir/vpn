import 'dart:io';

import 'package:fl_clash/common/path.dart';
import 'package:fl_clash/common/system.dart';
import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:proxy/proxy.dart';

Proxy? _proxy = system.isDesktop ? Proxy() : null;
bool _overridden = false;

Proxy? get proxy => _proxy;

Future<void> recoverSystemProxy() async {
  if (!system.isMacOS || _overridden) return;
  _proxy = Proxy(
    macosJournal: FileProxyJournal(
      () async => Directory('${await appPath.homeDirPath}/proxy-recovery'),
    ),
  );
  if (!await _proxy!.stopProxy()) {
    throw const FileSystemException(
      'System proxy recovery failed. Close other instances, check macOS '
      'Network proxy settings and reopen the app. Keep the proxy-recovery '
      'directory for recovery; no new connection has been started.',
    );
  }
}

@visibleForTesting
void setProxyForTesting(Proxy value) {
  _overridden = true;
  _proxy = value;
}

import 'package:fl_clash/common/system.dart';
import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:proxy/proxy.dart';

Proxy? _proxy = system.isDesktop ? Proxy() : null;

Proxy? get proxy => _proxy;

@visibleForTesting
void setProxyForTesting(Proxy value) => _proxy = value;

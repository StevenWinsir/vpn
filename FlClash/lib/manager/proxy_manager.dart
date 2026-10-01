import 'package:fl_clash/common/proxy.dart';
import 'package:fl_clash/common/print.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:fl_clash/models/models.dart';
import 'package:fl_clash/providers/providers.dart';
import 'package:material_ui/material_ui.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class ProxyManager extends ConsumerStatefulWidget {
  final Widget child;

  const ProxyManager({super.key, required this.child});

  @override
  ConsumerState createState() => _ProxyManagerState();
}

class _ProxyManagerState extends ConsumerState<ProxyManager> {
  Future<void> _pendingUpdate = Future.value();
  int _revision = 0;

  Future<void> _updateProxy(ProxyState proxyState) async {
    final isStart = proxyState.isStart;
    final systemProxy = proxyState.systemProxy;
    final port = proxyState.port;
    bool? result;
    if (isStart && systemProxy) {
      result = await proxy?.startProxy(port, proxyState.bassDomain);
    } else {
      result = await proxy?.stopProxy();
    }
    if (result == false) {
      commonPrint.log('update system proxy failed', logLevel: LogLevel.warning);
      if (mounted && isStart && systemProxy && ref.read(isStartProvider)) {
        await ref.read(setupActionProvider.notifier).setRunning(false);
      }
    }
  }

  void _scheduleUpdateProxy(ProxyState proxyState) {
    final revision = ++_revision;
    _pendingUpdate = _pendingUpdate
        .then((_) async {
          if (!mounted || revision != _revision) return;
          await _updateProxy(proxyState);
        })
        .catchError((Object error) async {
          commonPrint.log(
            'update system proxy failed',
            logLevel: LogLevel.warning,
          );
          if (mounted &&
              revision == _revision &&
              proxyState.isStart &&
              proxyState.systemProxy) {
            await ref.read(setupActionProvider.notifier).setRunning(false);
          }
        });
  }

  @override
  void initState() {
    super.initState();
    ref.listenManual(proxyStateProvider, (prev, next) {
      if (prev != next) {
        _scheduleUpdateProxy(next);
      }
    }, fireImmediately: true);
  }

  @override
  Widget build(BuildContext context) {
    return widget.child;
  }
}

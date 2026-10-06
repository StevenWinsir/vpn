import 'dart:async';

import 'package:fl_clash/core/controller.dart';
import 'package:fl_clash/core/method.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:fl_clash/providers/managed_account.dart';
import 'package:fl_clash/providers/providers.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/managed_fakes.dart';

class _InitializingCore extends CoreController {
  _InitializingCore(this.result) : super.scoped(ManagedCoreFake());
  final Completer<bool> result;

  @override
  FutureOr<bool> get isInit => false;

  @override
  Future<bool> init(int version) => result.future;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  for (final accepted in [false, true]) {
    test('restart awaits initialization acknowledgement: $accepted', () async {
      final initialized = Completer<bool>();
      final container = ProviderContainer(
        overrides: [
          coreHandlerProvider.overrideWithValue(_InitializingCore(initialized)),
        ],
      );
      addTearDown(container.dispose);
      container.read(managedAccountProvider);
      final pending = container.read(coreActionProvider.notifier).restartCore();
      final completed = accepted
          ? expectLater(pending, completion(isTrue))
          : expectLater(pending, throwsA(isA<CoreMethodException>()));
      await Future<void>.delayed(Duration.zero);
      expect(container.read(coreStatusProvider), CoreStatus.connecting);
      expect(container.read(managedAccountProvider).ready, isFalse);
      expect(container.read(managedAccountProvider).busy, isTrue);
      initialized.complete(accepted);
      await completed;
      await Future<void>.delayed(Duration.zero);
      expect(
        container.read(coreStatusProvider),
        accepted ? CoreStatus.connected : CoreStatus.disconnected,
      );
      expect(container.read(managedAccountProvider).ready, accepted);
      expect(
        container.read(managedAccountProvider).diagnostic,
        accepted ? '' : 'core_initialization_failed',
      );
    });
  }

  test(
    'a crash while initializing cannot be overwritten by a late success',
    () async {
      final initialized = Completer<bool>();
      final container = ProviderContainer(
        overrides: [
          coreHandlerProvider.overrideWithValue(_InitializingCore(initialized)),
        ],
      );
      addTearDown(container.dispose);
      container.read(managedAccountProvider);
      final pending = container.read(coreActionProvider.notifier).restartCore();
      await Future<void>.delayed(Duration.zero);
      container.read(coreStatusProvider.notifier).value =
          CoreStatus.disconnected;
      initialized.complete(true);
      expect(await pending, isFalse);
      expect(container.read(coreStatusProvider), CoreStatus.disconnected);
      expect(container.read(managedAccountProvider).ready, isFalse);
    },
  );
}

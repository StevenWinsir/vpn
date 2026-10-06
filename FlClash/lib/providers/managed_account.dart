import 'dart:async';
import 'dart:io';
import 'dart:math';

import 'package:fl_clash/common/managed_credentials.dart';
import 'package:fl_clash/core/controller.dart';
import 'package:fl_clash/core/desktop/model.dart';
import 'package:fl_clash/core/method.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:fl_clash/models/managed_account.dart';
import 'package:fl_clash/providers/providers.dart';
import 'package:fl_clash/state.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:riverpod_annotation/riverpod_annotation.dart';
import 'package:shared_preferences/shared_preferences.dart';

part 'generated/managed_account.g.dart';

final managedCredentialStoreProvider = Provider<ManagedCredentialStore>(
  (ref) => createManagedCredentialStore(),
);
final managedAppVersionProvider = Provider<String>(
  (ref) => globalState.packageInfo.version,
);
final managedConnectionAllowedProvider = Provider<bool>(
  (ref) => ref.watch(managedAccountProvider).account.canConnect,
);

@Riverpod(keepAlive: true)
Future<String> managedInstallationId(Ref ref) async {
  final preferences = await SharedPreferences.getInstance();
  const key = 'managed_installation_id';
  final saved = preferences.getString(key);
  if (saved != null &&
      RegExp(
        r'^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
      ).hasMatch(saved)) {
    return saved;
  }
  final random = Random.secure();
  final bytes = List<int>.generate(16, (_) => random.nextInt(256));
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  final hex = bytes
      .map((value) => value.toRadixString(16).padLeft(2, '0'))
      .join();
  final id =
      '${hex.substring(0, 8)}-${hex.substring(8, 12)}-${hex.substring(12, 16)}-${hex.substring(16, 20)}-${hex.substring(20)}';
  if (!await preferences.setString(key, id)) {
    throw StateError('Installation identity unavailable');
  }
  return id;
}

class ManagedAccountState {
  const ManagedAccountState({
    this.account = const ManagedAccountSnapshot(),
    this.ready = false,
    this.working = false,
    this.error = '',
    this.diagnostic = '',
  });

  final ManagedAccountSnapshot account;
  final bool ready;
  final bool working;
  final String error;
  final String diagnostic;

  bool get busy => working || account.busy;
  String get errorCode => error.isEmpty ? account.errorCode : error;
}

@Riverpod(keepAlive: true)
class ManagedAccount extends _$ManagedAccount {
  int _epoch = 0;
  bool _polling = false;
  Timer? _timer;

  CoreController get _core => ref.read(coreHandlerProvider);

  @override
  ManagedAccountState build() {
    ref.listen(coreStatusProvider, (previous, next) {
      if (next == CoreStatus.connecting) {
        forget(reason: '', working: true);
      } else if (next != CoreStatus.connected) {
        forget();
      } else if (previous != CoreStatus.connected) {
        unawaited(coldStart());
      }
    });
    ref.onDispose(() {
      _epoch++;
      _timer?.cancel();
      _timer = null;
    });
    return const ManagedAccountState();
  }

  bool _current(int epoch) => ref.mounted && epoch == _epoch;

  void _syncPolling() {
    if (!state.ready || state.account.user == null) {
      _timer?.cancel();
      _timer = null;
      return;
    }
    _timer ??= Timer.periodic(
      const Duration(seconds: 2),
      (_) => unawaited(poll()),
    );
  }

  void _isolateLegacyState() {
    _timer?.cancel();
    _timer = null;
    ref.read(currentPageLabelProvider.notifier).toPage(PageLabel.dashboard);
    ref.read(currentProfileIdProvider.notifier).value = null;
    ref.read(groupsProvider.notifier).value = [];
  }

  void forget({
    String reason = 'core_unavailable',
    bool working = false,
    String diagnostic = '',
  }) {
    _isolateLegacyState();
    _epoch++;
    state = ManagedAccountState(
      error: reason,
      working: working,
      diagnostic: diagnostic,
    );
    ref.read(runTimeProvider.notifier).value = null;
  }

  void failCore(Object error) {
    final code = switch (error) {
      DesktopCoreFailure(:final code) => code,
      CoreMethodException(code: 'managed_error', :final message) => message,
      CoreMethodException(:final code) => code,
      FileSystemException() => 'core_storage_error',
      ProcessException() => 'core_process_error',
      TimeoutException() => 'core_timeout',
      _ => 'core_unknown_error',
    };
    const allowed = {
      'core_initialization_failed',
      'no_response',
      'transport_disconnected',
      'transport_error',
      'invalid_server_response',
      'configuration_cleanup_failed',
      'configuration_storage_failed',
      'start_failed',
      'stop_failed',
      'process_exit_unconfirmed',
      'process_stop_failed',
      'unexpected_disconnect',
      'transport_failed',
      'core_storage_error',
      'core_process_error',
      'core_timeout',
    };
    forget(diagnostic: allowed.contains(code) ? code : 'core_unknown_error');
  }

  Future<void> coldStart() async {
    _isolateLegacyState();
    final epoch = ++_epoch;
    state = const ManagedAccountState(working: true);
    ref.read(runTimeProvider.notifier).value = null;
    try {
      await _core.stopListener();
      if (!_current(epoch)) return;
      final account = await _core.managedReset();
      if (_current(epoch)) {
        state = ManagedAccountState(account: account, ready: true);
      }
    } catch (error) {
      if (_current(epoch)) {
        failCore(error);
      }
    }
  }

  Future<void> login(
    String email,
    String password, {
    bool remember = false,
  }) async {
    if (!state.ready || state.busy) return;
    final scope = state.account.apiBase;
    final operationEpoch = _epoch + 1;
    await _perform((epoch) async {
      final deviceId = await ref.read(managedInstallationIdProvider.future);
      if (!_current(epoch)) {
        throw const CoreMethodException(
          code: 'operation_superseded',
          message: 'Request superseded',
        );
      }
      return _core.managedLogin(
        ManagedLoginRequest(
          email: email.trim(),
          password: password,
          deviceId: deviceId,
          platform: Platform.isMacOS ? 'macos' : Platform.operatingSystem,
          appVersion: ref.read(managedAppVersionProvider),
        ),
      );
    }, loadProfile: true);
    if (!_current(operationEpoch) ||
        state.account.user?.email != email.trim().toLowerCase()) {
      return;
    }
    final store = ref.read(managedCredentialStoreProvider);
    if (!store.supported || !isManagedCredentialScope(scope)) return;
    try {
      if (remember) {
        await store.save(
          scope,
          RememberedManagedLogin(email.trim().toLowerCase(), password),
        );
      } else {
        await store.delete(scope);
      }
    } catch (_) {
      if (_current(operationEpoch)) {
        state = ManagedAccountState(
          account: state.account,
          ready: state.ready,
          error: 'credential_store_unavailable',
        );
      }
    }
  }

  Future<bool> _forgetSavedLogin(String scope) async {
    final store = ref.read(managedCredentialStoreProvider);
    if (!store.supported || !isManagedCredentialScope(scope)) return true;
    try {
      await store.delete(scope);
      return true;
    } catch (_) {
      return false;
    }
  }

  Future<void> refresh() async {
    if (!state.ready || state.busy || state.account.user == null) return;
    final generation = state.account.generation;
    await _perform(
      (_) => _core.managedStatus(refresh: true, generation: generation),
      loadProfile: true,
    );
  }

  Future<bool> connect(int mixedPort) async {
    if (!state.ready || state.busy || state.account.configuration == null) {
      return false;
    }
    final generation = state.account.generation;
    await _perform((epoch) async {
      final latest = await _core.managedStatus();
      if (!_current(epoch) || latest.generation != generation) {
        throw const CoreMethodException(
          code: 'operation_superseded',
          message: 'Request superseded',
        );
      }
      return _core.managedConnect(
        generation,
        mixedPort,
        latest.runtimeRevision,
      );
    });
    return state.account.generation == generation && state.account.canConnect;
  }

  Future<void> disconnect() async {
    final account = state.account;
    if (account.user == null) return;
    await _perform((_) => _core.managedDisconnect(account.generation));
  }

  Future<void> reloadConfiguration() async {
    if (!state.ready || state.busy || state.account.user == null) return;
    final account = state.account;
    state = ManagedAccountState(
      ready: true,
      account: ManagedAccountSnapshot(
        apiBase: account.apiBase,
        generation: account.generation,
        phase: ManagedPhase.loadingConfiguration,
        user: account.user,
        session: account.session,
      ),
    );
    await _perform((_) => _core.managedLoadConfig(account.generation));
  }

  Future<void> selectProxy(String group, String proxy) async {
    final account = state.account;
    final configuration = account.configuration;
    if (!state.ready || state.busy || configuration == null) return;
    await _perform(
      (_) => _core.managedSelectProxy(
        generation: account.generation,
        configurationId: configuration.id,
        group: group,
        proxy: proxy,
      ),
    );
  }

  Future<void> _perform(
    Future<ManagedAccountSnapshot> Function(int epoch) operation, {
    bool loadProfile = false,
  }) async {
    final epoch = ++_epoch;
    state = ManagedAccountState(
      account: state.account,
      ready: true,
      working: true,
    );
    try {
      var account = await operation(epoch);
      if (!_current(epoch)) return;
      if (loadProfile &&
          (account.phase == ManagedPhase.profileRequired ||
              const {
                'profile_changed',
                'client_config_unavailable',
              }.contains(account.errorCode))) {
        state = ManagedAccountState(
          account: account,
          ready: true,
          working: true,
        );
        account = await _core.managedLoadConfig(account.generation);
        if (!_current(epoch)) return;
      }
      state = ManagedAccountState(account: account, ready: true);
    } catch (error) {
      if (!_current(epoch)) return;
      final code = error is CoreMethodException ? error.code : 'request_failed';
      if (const {
        'no_response',
        'transport_disconnected',
        'transport_error',
        'invalid_server_response',
      }.contains(code)) {
        if (code == 'invalid_server_response') {
          try {
            await _core.stopListener();
          } catch (_) {}
          if (!_current(epoch)) return;
          forget(reason: code);
        } else {
          forget();
        }
      } else {
        ManagedAccountSnapshot? stopped;
        try {
          await _core.stopListener();
          stopped = await _core.managedStatus();
        } catch (_) {}
        if (!_current(epoch)) return;
        state = ManagedAccountState(
          account: stopped ?? const ManagedAccountSnapshot(),
          ready: true,
          error: code,
        );
      }
    } finally {
      if (_current(epoch)) _syncPolling();
    }
  }

  Future<void> logout({bool forgetSavedLogin = true}) async {
    _isolateLegacyState();
    final account = state.account;
    final epoch = ++_epoch;
    final forgetting = forgetSavedLogin
        ? _forgetSavedLogin(account.apiBase)
        : Future<bool>.value(true);
    state = const ManagedAccountState(working: true);
    ref.read(runTimeProvider.notifier).value = null;
    try {
      final result = account.user == null
          ? await _core.managedReset()
          : await _core.managedLogout(account.generation);
      if (!_current(epoch)) return;
      await _core.stopListener();
      final forgotten = await forgetting;
      if (_current(epoch)) {
        state = ManagedAccountState(
          account: result,
          ready: true,
          error: forgotten ? '' : 'credential_store_unavailable',
        );
      }
    } catch (_) {
      await forgetting;
      if (_current(epoch)) {
        state = const ManagedAccountState(error: 'logout_unconfirmed');
      }
    }
  }

  Future<void> poll() async {
    if (!state.ready || state.working || _polling) return;
    final epoch = _epoch;
    _polling = true;
    try {
      final account = await _core.managedStatus();
      if (_current(epoch) && !state.working) {
        state = ManagedAccountState(account: account, ready: true);
      }
    } catch (_) {
      if (_current(epoch)) forget();
    } finally {
      _polling = false;
      if (ref.mounted) _syncPolling();
    }
  }
}

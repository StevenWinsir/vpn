import 'dart:convert';

import 'package:fl_clash/common/common.dart';
import 'package:fl_clash/common/managed_credentials.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:fl_clash/models/managed_account.dart';
import 'package:fl_clash/pages/home.dart';
import 'package:fl_clash/providers/managed_account.dart';
import 'package:fl_clash/providers/providers.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:material_ui/material_ui.dart';
import 'package:url_launcher/url_launcher.dart';

class ManagedAccountPage extends ConsumerWidget {
  const ManagedAccountPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(managedAccountProvider);
    return state.account.configuration != null &&
            !state.errorCode.startsWith('managed_tun_')
        ? const HomePage()
        : const ManagedAccountPanel();
  }
}

class ManagedAccountPanel extends ConsumerWidget {
  const ManagedAccountPanel({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(managedAccountProvider);
    final action = ref.read(managedAccountProvider.notifier);
    return ManagedAccountView(
      state: state,
      running: ref.watch(isStartProvider),
      onConnect: () async {
        await ref.read(setupActionProvider.notifier).setRunning(true);
      },
      onDisconnect: () async {
        await ref.read(setupActionProvider.notifier).setRunning(false);
      },
      onLogin: action.login,
      onLoginRemembered: (email, password, remember) =>
          action.login(email, password, remember: remember),
      credentialStore: ref.read(managedCredentialStoreProvider),
      onRefresh: action.refresh,
      onReloadConfiguration: action.reloadConfiguration,
      onLogout: action.logout,
      onRetryCore: () => ref.read(coreActionProvider.notifier).startCore(),
      onExit: () => ref.read(systemActionProvider.notifier).handleExit(),
      onAuthorizeTun: system.isMacOS
          ? () async {
              final result = await system.authorizeCore();
              if (result == AuthorizeCode.error) {
                throw StateError('TUN authorization declined');
              }
              if (!context.mounted) return;
              final restarted = await ref
                  .read(coreActionProvider.notifier)
                  .restartCore();
              if (!restarted) throw StateError('Core restart failed');
            }
          : null,
      onWebsite: () async {
        if (!await launchUrl(
          Uri.parse('https://test.hyshentou.cn/plans'),
          mode: LaunchMode.externalApplication,
        )) {
          throw StateError('Website unavailable');
        }
      },
    );
  }
}

class ManagedAccountView extends StatefulWidget {
  const ManagedAccountView({
    super.key,
    required this.state,
    this.running = false,
    this.onConnect,
    this.onDisconnect,
    required this.onLogin,
    this.onLoginRemembered,
    this.credentialStore,
    required this.onRefresh,
    this.onReloadConfiguration,
    required this.onLogout,
    required this.onRetryCore,
    required this.onExit,
    required this.onWebsite,
    this.onAuthorizeTun,
  });

  final ManagedAccountState state;
  final bool running;
  final Future<void> Function()? onConnect;
  final Future<void> Function()? onDisconnect;
  final Future<void> Function(String email, String password) onLogin;
  final Future<void> Function(String email, String password, bool remember)?
  onLoginRemembered;
  final ManagedCredentialStore? credentialStore;
  final Future<void> Function() onRefresh;
  final Future<void> Function()? onReloadConfiguration;
  final Future<void> Function() onLogout;
  final Future<void> Function() onRetryCore;
  final Future<void> Function() onExit;
  final Future<void> Function() onWebsite;
  final Future<void> Function()? onAuthorizeTun;

  @override
  State<ManagedAccountView> createState() => _ManagedAccountViewState();
}

class _ManagedAccountViewState extends State<ManagedAccountView> {
  final _form = GlobalKey<FormState>();
  final _email = TextEditingController();
  final _password = TextEditingController();
  bool _submitting = false;
  bool _actionFailed = false;
  bool _actionSubmitting = false;
  bool _remember = false;
  bool _credentialsError = false;
  bool _credentialsTouched = false;
  int _credentialRevision = 0;

  bool get _canRemember =>
      widget.credentialStore?.supported == true &&
      widget.onLoginRemembered != null &&
      isManagedCredentialScope(widget.state.account.apiBase);

  @override
  void initState() {
    super.initState();
    _loadSavedLogin();
  }

  Future<void> _loadSavedLogin() async {
    final revision = ++_credentialRevision;
    if (!_canRemember || widget.state.account.user != null) return;
    try {
      final saved = await widget.credentialStore!.read(
        widget.state.account.apiBase,
      );
      if (!mounted ||
          revision != _credentialRevision ||
          _credentialsTouched ||
          _submitting ||
          widget.state.account.user != null ||
          _email.text.isNotEmpty ||
          _password.text.isNotEmpty) {
        return;
      }
      if (saved != null) {
        setState(() {
          _email.text = saved.email;
          _password.text = saved.password;
          _remember = true;
        });
      }
    } catch (_) {
      if (mounted && revision == _credentialRevision) {
        setState(() => _credentialsError = true);
      }
    }
  }

  Future<void> _setRemember(bool remember) async {
    setState(() {
      _remember = remember;
      _credentialsTouched = true;
      _credentialRevision++;
    });
    if (remember || !_canRemember) return;
    final revision = _credentialRevision;
    try {
      await widget.credentialStore!.delete(widget.state.account.apiBase);
    } catch (_) {
      if (mounted && revision == _credentialRevision) {
        setState(() => _credentialsError = true);
      }
    }
  }

  @override
  void didUpdateWidget(covariant ManagedAccountView oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.state.account.apiBase != widget.state.account.apiBase ||
        oldWidget.credentialStore != widget.credentialStore) {
      _email.clear();
      _password.clear();
      _remember = false;
      _credentialsTouched = false;
      _credentialsError = false;
      _loadSavedLogin();
    }
    if (oldWidget.state.account.user?.id != widget.state.account.user?.id) {
      _credentialRevision++;
      _email.clear();
      _password.clear();
    }
  }

  @override
  void dispose() {
    _email.dispose();
    _password.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (_submitting ||
        widget.state.busy ||
        !widget.state.ready ||
        !_form.currentState!.validate()) {
      return;
    }
    final email = _email.text.trim();
    final password = _password.text;
    _password.clear();
    FocusManager.instance.primaryFocus?.unfocus();
    setState(() {
      _submitting = true;
      _actionFailed = false;
    });
    try {
      if (_canRemember) {
        await widget.onLoginRemembered!(email, password, _remember);
      } else {
        await widget.onLogin(email, password);
      }
    } catch (_) {
      if (mounted) setState(() => _actionFailed = true);
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  Future<void> _action(Future<void> Function() action) async {
    if (_actionSubmitting) return;
    setState(() {
      _actionFailed = false;
      _actionSubmitting = true;
    });
    try {
      await action();
    } catch (_) {
      if (mounted) setState(() => _actionFailed = true);
    } finally {
      if (mounted) setState(() => _actionSubmitting = false);
    }
  }

  String _message(BuildContext context, String code) {
    final strings = context.appLocalizations;
    return switch (code) {
      'managed_tun_permission_required' => strings.managedTunPermission,
      'managed_tun_start_failed' ||
      'managed_tun_cleanup_failed' => strings.managedTunFailed,
      'invalid_credentials' => strings.managedCredentialsError,
      'credential_store_unavailable' => strings.managedCredentialStoreError,
      'native_session_required' ||
      'native_session_expired' ||
      'subscription_changed' => strings.managedSessionExpired,
      'upgrade_required' ||
      'paid_vip_required' => strings.managedUpgradeRequired,
      'subscription_expired' => strings.managedSubscriptionExpired,
      'quota_exhausted' => strings.managedQuotaExhausted,
      'device_limit' => strings.managedDeviceLimit,
      'profile_changed' ||
      'client_config_unavailable' => strings.managedConfigUnavailable,
      'invalid_server_response' ||
      'invalid_client_config' => strings.managedProtocolError,
      'unsupported_managed_configuration' => strings.managedUnsupported,
      'configuration_storage_failed' ||
      'configuration_cleanup_failed' ||
      'configuration_apply_failed' => strings.managedStorageError,
      'configuration_expired' => strings.managedConfigUnavailable,
      'operation_in_progress' => strings.managedBusy,
      'logout_unconfirmed' => strings.managedLogoutUnconfirmed,
      'traffic_unconfirmed' => strings.managedTrafficUnconfirmed,
      'final_traffic_unconfirmed' => strings.managedFinalTrafficUnconfirmed,
      'invalid_core_counters' => strings.managedCounterError,
      'lease_expired' ||
      'lifecycle_reconfirmation_required' => strings.managedReconnectRequired,
      'core_unavailable' => strings.managedCoreUnavailable,
      'heartbeat_unavailable' ||
      'heartbeat_rejected' ||
      'auth_unavailable' ||
      'session_failed' ||
      'rate_limited' => strings.managedNetworkError,
      _ => strings.managedRequestFailed,
    };
  }

  @override
  Widget build(BuildContext context) {
    final strings = context.appLocalizations;
    final state = widget.state;
    final account = state.account;
    final user = account.user;
    final session = account.session;
    final busy = state.busy || _submitting || _actionSubmitting;
    final error = state.errorCode;
    return PopScope(
      canPop: false,
      child: Scaffold(
        body: SafeArea(
          child: Center(
            child: SingleChildScrollView(
              padding: const EdgeInsets.all(24),
              child: ConstrainedBox(
                constraints: const BoxConstraints(maxWidth: 480),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    const Icon(Icons.lock_outline, size: 48),
                    const SizedBox(height: 16),
                    Text(
                      user == null
                          ? strings.managedSignInTitle
                          : strings.managedAccountTitle,
                      style: context.textTheme.headlineSmall,
                      textAlign: TextAlign.center,
                    ),
                    const SizedBox(height: 12),
                    Text(
                      account.canConnect
                          ? widget.running
                                ? strings.managedConnected
                                : strings.managedConnecting
                          : strings.managedStopped,
                      key: const Key('managed-connection-state'),
                      textAlign: TextAlign.center,
                    ),
                    const SizedBox(height: 24),
                    if (busy)
                      const LinearProgressIndicator()
                    else
                      const SizedBox(height: 4),
                    const SizedBox(height: 16),
                    if (widget.onAuthorizeTun != null && !widget.running) ...[
                      Text(strings.managedTunDescription),
                      const SizedBox(height: 12),
                      OutlinedButton.icon(
                        key: const Key('managed-authorize-tun'),
                        onPressed: busy
                            ? null
                            : () => _action(widget.onAuthorizeTun!),
                        icon: const Icon(Icons.admin_panel_settings_outlined),
                        label: Text(strings.managedTunAuthorize),
                      ),
                      const SizedBox(height: 16),
                    ],
                    if (!state.ready) ...[
                      Text(
                        state.working
                            ? strings.managedCoreStarting
                            : strings.managedCoreUnavailable,
                      ),
                      const SizedBox(height: 12),
                      FilledButton(
                        onPressed: busy
                            ? null
                            : () => _action(widget.onRetryCore),
                        child: Text(strings.managedRetryCore),
                      ),
                    ] else if (user == null) ...[
                      Text(strings.managedIntro),
                      const SizedBox(height: 20),
                      Form(
                        key: _form,
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.stretch,
                          children: [
                            TextFormField(
                              key: const Key('managed-email'),
                              controller: _email,
                              onChanged: (_) => _credentialsTouched = true,
                              enabled: !busy,
                              decoration: InputDecoration(
                                labelText: strings.managedEmail,
                              ),
                              keyboardType: TextInputType.emailAddress,
                              textInputAction: TextInputAction.next,
                              autocorrect: false,
                              validator: (value) {
                                final email = value?.trim() ?? '';
                                return utf8.encode(email).length <= 254 &&
                                        RegExp(
                                          r'^[^\s@]+@[^\s@]+\.[^\s@]+$',
                                        ).hasMatch(email)
                                    ? null
                                    : strings.managedInvalidEmail;
                              },
                            ),
                            const SizedBox(height: 16),
                            TextFormField(
                              key: const Key('managed-password'),
                              controller: _password,
                              onChanged: (_) => _credentialsTouched = true,
                              enabled: !busy,
                              decoration: InputDecoration(
                                labelText: strings.managedPassword,
                              ),
                              obscureText: true,
                              enableSuggestions: false,
                              autocorrect: false,
                              enableIMEPersonalizedLearning: false,
                              textInputAction: TextInputAction.done,
                              onFieldSubmitted: (_) => _submit(),
                              validator: (value) =>
                                  value != null &&
                                      value.isNotEmpty &&
                                      utf8.encode(value).length <= 72
                                  ? null
                                  : strings.managedInvalidPassword,
                            ),
                            if (_canRemember) ...[
                              CheckboxListTile(
                                key: const Key('managed-remember'),
                                contentPadding: EdgeInsets.zero,
                                controlAffinity:
                                    ListTileControlAffinity.leading,
                                value: _remember,
                                title: Text(strings.managedRememberPassword),
                                subtitle: Text(Uri.parse(account.apiBase).host),
                                onChanged: busy
                                    ? null
                                    : (value) => _setRemember(value ?? false),
                              ),
                              if (_credentialsError)
                                Text(strings.managedCredentialStoreError),
                            ],
                            const SizedBox(height: 24),
                            FilledButton(
                              key: const Key('managed-login'),
                              onPressed: busy ? null : _submit,
                              child: Text(strings.managedSignIn),
                            ),
                          ],
                        ),
                      ),
                    ] else ...[
                      Text(user.email, style: context.textTheme.titleMedium),
                      if (session != null) ...[
                        const SizedBox(height: 12),
                        Text(strings.managedPlan(session.planName)),
                        Text(
                          strings.managedBalance(
                            (session.remainingBytes / (1 << 20))
                                .toStringAsFixed(2),
                            (session.totalBytes / (1 << 20)).toStringAsFixed(2),
                          ),
                        ),
                        if (session.subscriptionExpiresAt != null)
                          Text(
                            strings.managedExpires(
                              MaterialLocalizations.of(
                                context,
                              ).formatMediumDate(
                                session.subscriptionExpiresAt!.toLocal(),
                              ),
                            ),
                          ),
                      ],
                      const SizedBox(height: 20),
                      if (account.phase ==
                          ManagedPhase.configurationApplied) ...[
                        Text(strings.managedApplied),
                        const SizedBox(height: 12),
                        FilledButton.icon(
                          key: const Key('managed-connect'),
                          onPressed:
                              busy ||
                                  account.canConnect ||
                                  widget.onConnect == null
                              ? null
                              : () => _action(widget.onConnect!),
                          icon: const Icon(Icons.play_arrow),
                          label: Text(strings.managedConnect),
                        ),
                        OutlinedButton.icon(
                          key: const Key('managed-disconnect'),
                          onPressed:
                              (!busy && !account.canConnect) ||
                                  widget.onDisconnect == null
                              ? null
                              : () => _action(widget.onDisconnect!),
                          icon: const Icon(Icons.stop),
                          label: Text(strings.managedDisconnect),
                        ),
                        const SizedBox(height: 12),
                        OutlinedButton.icon(
                          key: const Key('managed-reload'),
                          onPressed:
                              busy || widget.onReloadConfiguration == null
                              ? null
                              : () => _action(widget.onReloadConfiguration!),
                          icon: const Icon(Icons.sync),
                          label: Text(strings.managedReload),
                        ),
                      ],
                      if (account.metering case final metering?) ...[
                        const SizedBox(height: 12),
                        Text(
                          strings.managedEstimatedBalance(
                            (metering.estimatedRemaining / (1 << 20))
                                .toStringAsFixed(2),
                          ),
                        ),
                        Text(
                          strings.managedCumulativeTraffic(
                            (metering.upload / (1 << 20)).toStringAsFixed(2),
                            (metering.download / (1 << 20)).toStringAsFixed(2),
                          ),
                        ),
                        if (metering.pending)
                          Text(strings.managedTrafficPending),
                      ],
                      if (account.phase == ManagedPhase.configurationStaged)
                        Text(strings.managedStaged)
                      else if (account.phase == ManagedPhase.profileRequired ||
                          account.phase == ManagedPhase.loadingConfiguration)
                        Text(strings.managedLoading),
                      const SizedBox(height: 20),
                      FilledButton(
                        onPressed: busy
                            ? null
                            : () => _action(widget.onRefresh),
                        child: Text(strings.managedCheck),
                      ),
                    ],
                    if (error.isNotEmpty || _actionFailed) ...[
                      const SizedBox(height: 16),
                      Semantics(
                        liveRegion: true,
                        child: Text(
                          _actionFailed
                              ? strings.managedRequestFailed
                              : _message(context, error),
                          style: TextStyle(color: context.colorScheme.error),
                        ),
                      ),
                    ],
                    const SizedBox(height: 16),
                    OutlinedButton(
                      onPressed: () => _action(widget.onWebsite),
                      child: Text(strings.managedWebsite),
                    ),
                    if (user != null || busy)
                      TextButton(
                        key: const Key('managed-logout'),
                        onPressed: () => _action(widget.onLogout),
                        child: Text(strings.managedSignOut),
                      ),
                    TextButton(
                      onPressed: () => _action(widget.onExit),
                      child: Text(strings.managedExitApp),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

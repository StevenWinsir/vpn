import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:fl_clash/core/desktop/model.dart';
import 'package:fl_clash/core/interface.dart';
import 'package:fl_clash/core/method.dart';

const managedFixtureDevice = '12345678-1234-4234-8234-123456789abc';

Map<String, dynamic> managedSnapshot({
  String phase = 'signed_out',
  String reason = '',
  int generation = 1,
}) {
  final cases =
      (jsonDecode(
                File(
                  'test/fixtures/native_client_contract.json',
                ).readAsStringSync(),
              )
              as Map)['cases']
          as Map;
  Map<String, dynamic>? session;
  if (phase != 'signed_out' && phase != 'authenticating') {
    final fixture =
        cases[const {
                  'configuration_staged',
                  'configuration_applied',
                }.contains(phase)
                ? 'vip_ready'
                : 'vip_unbound']
            as Map;
    final response = Map<String, dynamic>.from(fixture['response'] as Map);
    session = Map<String, dynamic>.from(
      (response['session'] ?? response) as Map,
    );
    if (reason.isNotEmpty) {
      session['reason'] = reason;
      session['can_connect'] = false;
      session['authorization_expires_at'] = null;
    }
  }
  return {
    'generation': generation,
    'phase': phase,
    'busy': phase == 'authenticating',
    'can_connect': false,
    'user': session == null
        ? null
        : {
            'id': 'user-$generation',
            'email': 'fixture@example.invalid',
            'name': 'Fixture',
          },
    'session': session,
    'profile_version':
        const {'configuration_staged', 'configuration_applied'}.contains(phase)
        ? session!['profile_version']
        : '',
    'error_code': reason,
    if (phase == 'configuration_applied')
      'configuration': {
        'id': '1234567890abcdef1234567890abcdef',
        'version': session!['profile_version'],
        'owner': {
          'generation': generation,
          'user_id': 'user-$generation',
          'session_id': session['session_id'],
        },
        'groups': [
          {
            'name': 'VIP',
            'type': 'select',
            'selected': 'Server-A',
            'proxies': ['Server-A', 'Server-B'],
          },
        ],
      },
  };
}

class ManagedCoreFake extends CoreHandlerInterface {
  final calls = <CoreMethodCall>[];
  Map<String, dynamic> account = managedSnapshot();
  String loginReason = 'profile_required';
  FutureOr<Object?> Function(CoreMethod method, Object? arguments)? respond;

  @override
  Future<CoreLifecycleResult> start() async => const CoreLifecycleResult(
    revision: 1,
    outcome: CoreLifecycleOutcome.applied,
  );

  @override
  Future<CoreLifecycleResult> restart() => start();

  @override
  Future<CoreLifecycleResult> stop() => start();

  @override
  Future<CoreLifecycleResult> close() => start();

  @override
  Future<T?> invokeMethod<T>({
    required CoreMethod method,
    Object? arguments,
    Duration? timeout,
  }) async {
    calls.add(
      CoreMethodCall(
        id: '${calls.length + 1}',
        method: method,
        arguments: arguments,
      ),
    );
    final response = respond;
    if (response != null) return await response(method, arguments) as T?;
    return defaultResponse(method, arguments) as T?;
  }

  Object? defaultResponse(CoreMethod method, Object? arguments) {
    switch (method) {
      case CoreMethod.managedTunReady:
      case CoreMethod.stopListener:
        return true;
      case CoreMethod.startListener:
        return false;
      case CoreMethod.managedLogin:
        account = managedSnapshot(
          phase: loginReason == 'profile_required'
              ? 'profile_required'
              : 'restricted',
          reason: loginReason == 'profile_required' ? '' : loginReason,
          generation: (account['generation'] as int) + 1,
        );
      case CoreMethod.managedLoadConfig:
        account = managedSnapshot(
          phase: 'configuration_applied',
          generation: account['generation'] as int,
        );
      case CoreMethod.managedSelectProxy:
        final params = arguments as Map;
        final configuration = account['configuration'] as Map;
        if (params['generation'] != account['generation'] ||
            params['configuration_id'] != configuration['id']) {
          throw const CoreMethodException(
            code: 'operation_superseded',
            message: 'Superseded',
          );
        }
        final group = (configuration['groups'] as List).cast<Map>().firstWhere(
          (group) => group['name'] == params['group'],
        );
        if (!(group['proxies'] as List).contains(params['proxy'])) {
          throw const CoreMethodException(
            code: 'invalid_managed_selection',
            message: 'Invalid selection',
          );
        }
        group['selected'] = params['proxy'];
      case CoreMethod.managedReset:
      case CoreMethod.managedLogout:
        account = managedSnapshot(
          generation: (account['generation'] as int) + 1,
        );
      default:
        break;
    }
    return account;
  }
}

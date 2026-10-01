import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:fl_clash/core/method.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  final fixture =
      jsonDecode(
            File(
              'test/fixtures/native_client_contract.json',
            ).readAsStringSync(),
          )
          as Map<String, dynamic>;
  final cases = fixture['cases'] as Map<String, dynamic>;
  const reasons = {
    'login': 'upgrade_required',
    'free': 'upgrade_required',
    'vip_unbound': 'profile_required',
    'vip_ready': '',
    'traffic_ready': '',
    'quota_exhausted': 'quota_exhausted',
  };
  const stateKeys = {
    'server_time',
    'subscription_expires_at',
    'authorization_expires_at',
    'session_idle_timeout_seconds',
    'profile_version',
    'session_id',
    'expires_at',
    'can_connect',
    'reason',
    'remaining_bytes',
    'total_bytes',
    'used_units',
    'plan_name',
    'last_sequence',
    'upload_bytes',
    'download_bytes',
    'report_interval_seconds',
    'lease_seconds',
    'metering_source',
    'rate_permille',
  };

  test('native fixture version and endpoint envelopes', () {
    expect(fixture['contract_version'], 1);
    expect(cases.length, 8);
    final login = cases['login']['response'] as Map<String, dynamic>;
    expect(login.keys, unorderedEquals({'token', 'user', 'session'}));
    expect(login['token'], matches(RegExp(r'^[A-Za-z0-9_-]{43}$')));
    expect(login['user'], isNot(contains('password')));
    expect(cases['free']['response'], isNot(contains('session')));
    final config = cases['vip_ready']['response'] as Map<String, dynamic>;
    expect(config.keys, unorderedEquals({'yaml', 'version', 'session'}));
    expect(
      sha256.convert(utf8.encode(config['yaml'] as String)).toString(),
      config['version'],
    );
    expect(config['session']['profile_version'], config['version']);
    expect(cases['quota_exhausted']['status'], 200);
    expect(cases['quota_exhausted']['response']['replayed'], false);
    expect(cases['logout']['response'], {'logged_out': true});
  });

  for (final entry in reasons.entries) {
    test('${entry.key}: typed fields and structured public RPC state', () {
      final example = cases[entry.key] as Map<String, dynamic>;
      final response = example['response'] as Map<String, dynamic>;
      final state = example['path'] == '/session'
          ? response
          : response['session'] as Map<String, dynamic>;
      expect(state.keys, unorderedEquals(stateKeys));
      expect(state['reason'], entry.value);
      expect(state['can_connect'], entry.value.isEmpty);
      expect(state['report_interval_seconds'], 60);
      expect(state['lease_seconds'], 90);
      expect(state['session_idle_timeout_seconds'], 180);
      expect(state['metering_source'], 'client_reported');
      expect(state['rate_permille'], 1000);
      for (final key in [
        'remaining_bytes',
        'total_bytes',
        'used_units',
        'last_sequence',
        'upload_bytes',
        'download_bytes',
      ]) {
        expect(state[key], isA<int>());
        expect(state[key] as int, greaterThanOrEqualTo(0));
      }
      if (state['can_connect'] as bool) {
        final serverTime = DateTime.parse(state['server_time'] as String);
        final cutoff = DateTime.parse(
          state['authorization_expires_at'] as String,
        );
        expect(cutoff.isAfter(serverTime), true);
        expect(cutoff.difference(serverTime).inSeconds, lessThanOrEqualTo(90));
        for (final key in ['expires_at', 'subscription_expires_at']) {
          expect(cutoff.isAfter(DateTime.parse(state[key] as String)), false);
        }
      } else {
        expect(state['authorization_expires_at'], isNull);
      }
      final rpc = CoreMethodResponse(id: entry.key, result: state);
      final decoded = CoreMethodResponse.fromJson(
        Map<String, Object?>.from(jsonDecode(jsonEncode(rpc.toJson())) as Map),
      );
      expect(decoded.unwrap<Map<String, dynamic>>(), state);
      expect(decoded.result, isNot(isA<String>()));
      expect(decoded.result, isNot(contains('token')));
      expect(decoded.result, isNot(contains('yaml')));
    });
  }

  test('expired HTTP session maps to structured RPC error', () {
    final example = cases['session_expired'] as Map<String, dynamic>;
    expect(example['status'], 401);
    final error = example['response']['error'] as Map<String, dynamic>;
    final rpc = CoreMethodResponse.fromJson({'id': 'expired', 'error': error});
    expect(
      () => rpc.unwrap<Object?>(),
      throwsA(
        isA<CoreMethodException>().having(
          (error) => error.code,
          'code',
          'native_session_expired',
        ),
      ),
    );
  });
}

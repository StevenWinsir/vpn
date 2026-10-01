import 'dart:convert';

import 'package:fl_clash/models/managed_account.dart';
import 'package:flutter_test/flutter_test.dart';

import '../helpers/managed_fakes.dart';

Map<String, dynamic> authorized() {
  final value = managedSnapshot(phase: 'configuration_applied');
  value['can_connect'] = true;
  (value['session'] as Map)['can_connect'] = true;
  value['runtime_revision'] = 4;
  value['metering'] = {
    'sequence': 1,
    'acknowledged_upload_bytes': 0,
    'acknowledged_download_bytes': 0,
    'upload_bytes': 100,
    'download_bytes': 200,
    'confirmed_remaining_bytes': 262144,
    'estimated_remaining_bytes': 261844,
    'pending': true,
  };
  return value;
}

void main() {
  test(
    'confirmed runtime exposes separate acknowledged and estimated allowance',
    () {
      final state = ManagedAccountSnapshot.fromJson(authorized());
      expect(state.canConnect, isTrue);
      expect(state.runtimeRevision, 4);
      expect(
        state.metering!.confirmedRemaining - state.metering!.estimatedRemaining,
        300,
      );
      expect(state.metering!.acknowledgedUpload, 0);
      expect(state.metering!.pending, isTrue);
    },
  );

  test(
    'missing runtime intent or metering cannot enable the UI connection',
    () {
      for (final key in ['runtime_revision', 'metering', 'configuration']) {
        final value = authorized()..remove(key);
        expect(
          () => ManagedAccountSnapshot.fromJson(value),
          throwsFormatException,
          reason: key,
        );
      }
    },
  );

  test('counter rollback overflow and zero local allowance fail closed', () {
    for (final change in [
      {'sequence': 0},
      {'sequence': (1 << 40) + 1},
      {'upload_bytes': -1},
      {'download_bytes': (1 << 50) + 1},
      {'acknowledged_upload_bytes': 101},
      {'estimated_remaining_bytes': 262145},
      {'estimated_remaining_bytes': 0},
    ]) {
      final value =
          jsonDecode(jsonEncode(authorized())) as Map<String, dynamic>;
      (value['metering'] as Map).addAll(change);
      expect(
        () => ManagedAccountSnapshot.fromJson(value),
        throwsFormatException,
        reason: '$change',
      );
    }
  });
}

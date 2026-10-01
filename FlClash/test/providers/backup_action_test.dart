import 'dart:convert';
import 'dart:io';

import 'package:drift/native.dart';
import 'package:fl_clash/database/database.dart';
import 'package:fl_clash/enum/enum.dart';
import 'package:fl_clash/models/models.dart';
import 'package:fl_clash/providers/action.dart';
import 'package:fl_clash/providers/config.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:riverpod/riverpod.dart';

class _StubbedArchiveBackupAction extends BackupAction {
  _StubbedArchiveBackupAction(this.archivePath);

  final String archivePath;

  @override
  Future<String> backup() async => archivePath;
}

Profile _profile(int id, String label) => Profile(
  id: id,
  label: label,
  autoUpdateDuration: Duration.zero,
  overwriteType: OverwriteType.standard,
);

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  late Database testDatabase;

  setUp(() {
    testDatabase = Database(NativeDatabase.memory());
    database = testDatabase;
  });

  tearDown(() async {
    await testDatabase.close();
  });

  ProviderContainer buildContainer() {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    return container;
  }

  BackupAction actionOf(ProviderContainer container) =>
      container.read(backupActionProvider.notifier);

  Map<String, Object?> configMapOf(ProviderContainer container) =>
      jsonDecode(jsonEncode(container.read(configProvider).toJson()))
          as Map<String, Object?>;

  group('consumeBackup owns the archive it produced', () {
    File stubArchive() {
      final directory = Directory.systemTemp.createTempSync('flclash_backup');
      addTearDown(() => directory.deleteSync(recursive: true));
      final file = File('${directory.path}/backup.zip')
        ..writeAsBytesSync(const [80, 75, 5, 6]);
      return file;
    }

    ProviderContainer containerFor(String archivePath) {
      final container = ProviderContainer(
        overrides: [
          backupActionProvider.overrideWith(
            () => _StubbedArchiveBackupAction(archivePath),
          ),
        ],
      );
      addTearDown(container.dispose);
      return container;
    }

    test('deletes the archive once it has been sent', () async {
      final archive = stubArchive();
      final container = containerFor(archive.path);
      String? sent;

      final result = await actionOf(container).consumeBackup((path) async {
        sent = path;
        return true;
      });

      expect(result, isTrue);
      expect(sent, archive.path);
      expect(archive.existsSync(), isFalse);
    });

    test('deletes the archive when sending it fails', () async {
      final archive = stubArchive();
      final container = containerFor(archive.path);

      await expectLater(
        actionOf(
          container,
        ).consumeBackup((_) async => throw const SocketException('offline')),
        throwsA(isA<SocketException>()),
      );

      expect(archive.existsSync(), isFalse);
    });
  });

  group('managed policy rejects backup and restore before side effects', () {
    test('archive creation, sending and unpacking are all blocked', () async {
      final container = buildContainer();
      var sent = false;
      await expectLater(actionOf(container).backup(), throwsStateError);
      await expectLater(
        actionOf(container).consumeBackup((_) async {
          sent = true;
          return true;
        }),
        throwsStateError,
      );
      await expectLater(
        actionOf(container).restore(RestoreOption.all),
        throwsStateError,
      );
      expect(sent, isFalse);
      expect(await testDatabase.profilesDao.query().get(), isEmpty);
    });

    for (final strategy in RestoreStrategy.values) {
      for (final option in RestoreOption.values) {
        for (final malformed in [false, true]) {
          test(
            '$strategy / $option / malformed=$malformed leaves all data intact',
            () async {
              final container = buildContainer();
              await testDatabase.profilesDao.putAll([
                _profile(9, 'Pre-existing').toCompanion(0),
              ]);
              container
                  .read(appSettingProvider.notifier)
                  .update((state) => state.copyWith(restoreStrategy: strategy));
              final before = configMapOf(container);
              final source = buildContainer();
              source.read(currentProfileIdProvider.notifier).value = 42;
              source
                  .read(appSettingProvider.notifier)
                  .update(
                    (state) => state.copyWith(autoRun: true, autoLaunch: true),
                  );
              source
                  .read(patchClashConfigProvider.notifier)
                  .update((state) => state.copyWith(mixedPort: 7899));
              final importedConfig = configMapOf(source);
              if (malformed) importedConfig['currentProfileId'] = 'not an int';
              await expectLater(
                actionOf(container).applyRestore(
                  MigrationData(
                    configMap: importedConfig,
                    profiles: [_profile(1, 'Imported')],
                    proxyGroups: const [
                      ProxyGroup(
                        id: 1,
                        profileId: 7,
                        name: 'Imported group',
                        type: GroupType.Selector,
                      ),
                    ],
                  ),
                  option,
                ),
                throwsStateError,
              );
              final stored = await testDatabase.profilesDao.query().get();
              expect(stored.map((item) => (item.id, item.label)), [
                (9, 'Pre-existing'),
              ]);
              expect(await testDatabase.proxyGroupsDao.query(7).get(), isEmpty);
              expect(configMapOf(container), before);
            },
          );
        }
      }
    }
  });
}

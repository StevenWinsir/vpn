import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:proxy/src/macos_proxy.dart';
import 'package:proxy/src/owned_settings.dart';
import 'package:proxy/src/proxy_command.dart';
import 'package:proxy/src/proxy_journal.dart';

String endpoint(String server, int port, {bool enabled = true}) => jsonEncode({
  'enabled': enabled,
  'server': server,
  'port': port,
  'authenticated': false,
});

class _Host {
  final values = {
    for (final kind in ['webproxy', 'securewebproxy', 'socksfirewallproxy'])
      kind: endpoint('old', 8888),
  };
  List<String> bypass = ['old.example'];
  int writes = 0;
  bool readable = true;

  Future<ProcessResult> run(
    String executable,
    List<String> args, {
    bool runInShell = false,
  }) async {
    expect(executable, '/usr/sbin/networksetup');
    expect(runInShell, isFalse);
    final option = args.first;
    if (!readable && option.startsWith('-get')) {
      return ProcessResult(1, 1, '', '');
    }
    if (option == '-listallnetworkservices') {
      return ProcessResult(1, 0, 'Wi-Fi\n', '');
    }
    if (option == '-getautoproxyurl') {
      return ProcessResult(1, 0, 'Enabled: No\n', '');
    }
    if (option == '-getproxybypassdomains') {
      return ProcessResult(1, 0, bypass.join('\n'), '');
    }
    if (option.startsWith('-get')) {
      final value = jsonDecode(values[option.substring(4)]!) as Map;
      return ProcessResult(
        1,
        0,
        'Enabled: ${value['enabled'] == true ? 'Yes' : 'No'}\nServer: ${value['server']}\nPort: ${value['port']}\nAuthenticated Proxy Enabled: 0\n',
        '',
      );
    }
    writes++;
    if (option == '-setproxybypassdomains') {
      bypass = args.sublist(2).where((value) => value != 'Empty').toList();
    } else if (option.endsWith('state')) {
      final key = option.substring(4, option.length - 5);
      values[key] = jsonEncode({
        ...jsonDecode(values[key]!) as Map,
        'enabled': args.last == 'on',
      });
    } else {
      values[option.substring(4)] = endpoint(args[2], int.parse(args[3]));
    }
    return ProcessResult(1, 0, '', '');
  }
}

class _MemoryJournal implements ProxyJournal {
  List<OwnedProxyRecord> records = [];
  bool writable = true;
  @override
  Future<List<OwnedProxyRecord>> read() async => records;
  @override
  Future<void> write(List<OwnedProxyRecord> value) async {
    if (!writable) throw const FileSystemException('test storage unavailable');
    records = value
        .map(
          (entry) =>
              OwnedProxyRecord.fromJson(jsonDecode(jsonEncode(entry.toJson()))),
        )
        .toList();
  }
}

void main() {
  test('journals before OS writes and restores across fresh owners', () async {
    final journal = _MemoryJournal();
    final host = _Host();
    final proxy = MacosProxy(
      journal: journal,
      commandRunner: ProxyCommandRunner((
        executable,
        args, {
        runInShell = false,
      }) async {
        if (args.first.startsWith('-set')) expect(journal.records, isNotEmpty);
        return host.run(executable, args);
      }),
    );
    expect(await proxy.start(7890, ['localhost']), isTrue);
    expect(journal.records, hasLength(4));
    final replacement = MacosProxy(
      journal: journal,
      commandRunner: ProxyCommandRunner(host.run),
    );
    expect(await replacement.stop(), isTrue);
    expect(host.values.values, everyElement(endpoint('old', 8888)));
    expect(host.bypass, ['old.example']);
    expect(journal.records, isEmpty);
  });

  test('unwritable journal prevents every OS mutation', () async {
    final journal = _MemoryJournal()..writable = false;
    final host = _Host();
    final proxy = MacosProxy(
      journal: journal,
      commandRunner: ProxyCommandRunner(host.run),
    );
    expect(await proxy.start(7890, []), isFalse);
    expect(host.writes, 0);
  });

  test(
    'failed recovery keeps its record and blocks a new connection',
    () async {
      final journal = _MemoryJournal();
      final host = _Host();
      expect(
        await MacosProxy(
          journal: journal,
          commandRunner: ProxyCommandRunner(host.run),
        ).start(7890, []),
        isTrue,
      );
      host.readable = false;
      final writes = host.writes;
      final replacement = MacosProxy(
        journal: journal,
        commandRunner: ProxyCommandRunner(host.run),
      );
      expect(await replacement.start(7891, []), isFalse);
      expect(host.writes, writes);
      expect(journal.records, isNotEmpty);
      host.readable = true;
      expect(await replacement.stop(), isTrue);
      expect(journal.records, isEmpty);
      expect(host.values.values, everyElement(endpoint('old', 8888)));
    },
  );

  test('restart recovery preserves another software endpoint', () async {
    final journal = _MemoryJournal();
    final host = _Host();
    expect(
      await MacosProxy(
        journal: journal,
        commandRunner: ProxyCommandRunner(host.run),
      ).start(7890, []),
      isTrue,
    );
    host.values['webproxy'] = endpoint('external', 9000);
    expect(
      await MacosProxy(
        journal: journal,
        commandRunner: ProxyCommandRunner(host.run),
      ).stop(),
      isTrue,
    );
    expect(host.values['webproxy'], endpoint('external', 9000));
    expect(host.values['securewebproxy'], endpoint('old', 8888));
  });

  test(
    'unknown setting and invalid endpoint fail closed without deleting evidence',
    () async {
      for (final record in [
        OwnedProxyRecord('Wi-Fi/arbitrary', endpoint('old', 8888), [
          endpoint('127.0.0.1', 7890),
        ]),
        const OwnedProxyRecord('Wi-Fi/webproxy', '{"server":"bad"}', ['{}']),
      ]) {
        final journal = _MemoryJournal()..records = [record];
        final host = _Host();
        final proxy = MacosProxy(
          journal: journal,
          commandRunner: ProxyCommandRunner(host.run),
        );
        expect(await proxy.start(7890, []), isFalse);
        expect(host.writes, 0);
        expect(journal.records, hasLength(1));
      }
    },
  );

  test(
    'partial restore can be resumed instead of mistaken for external ownership',
    () async {
      var value = 'original', failRestore = true;
      final journal = _MemoryJournal();
      ProxySetting setting() => ProxySetting(
        key: 'key',
        target: 'managed',
        read: () async => value,
        intermediate: (_, target) => ['$target-partial'],
        write: (target) async {
          value = target == 'original' && failRestore
              ? 'original-partial'
              : target;
          return !(target == 'original' && failRestore);
        },
      );
      final first = OwnedProxySettings(checkpoint: journal.write);
      expect(await first.apply([setting()]), isTrue);
      expect(await first.restore(), isFalse);
      final recovered = OwnedProxySettings(checkpoint: journal.write);
      expect(
        await recovered.recover(journal.records, (_) => setting()),
        isTrue,
      );
      failRestore = false;
      expect(await recovered.restore(), isTrue);
      expect(value, 'original');
      expect(journal.records, isEmpty);
    },
  );

  group('private disk journal', () {
    late Directory root;
    late FileProxyJournal journal;
    setUp(() async {
      root = await Directory.systemTemp.createTemp('flclash-journal-test-');
      journal = FileProxyJournal(
        () async => Directory('${root.path}/recovery'),
      );
    });
    tearDown(() async {
      await journal.close();
      await root.delete(recursive: true);
    });

    test(
      'atomically persists private records without leftover staging data',
      () async {
        final record = OwnedProxyRecord(
          'Wi-Fi/webproxy',
          endpoint('old', 8888),
          [endpoint('127.0.0.1', 7890)],
        );
        await journal.write([record]);
        expect((await journal.read()).single.toJson(), record.toJson());
        final file = File('${root.path}/recovery/settings.json');
        expect((await file.stat()).mode & 0x1ff, 0x180);
        expect(
          await File('${root.path}/recovery/settings.pending').exists(),
          isFalse,
        );
        await journal.write([]);
        expect(await file.exists(), isFalse);
      },
    );

    test(
      'corrupt and future-version data is never silently discarded',
      () async {
        await journal.read();
        final file = File('${root.path}/recovery/settings.json');
        for (final content in [
          '{',
          '{"version":2,"records":[]}',
          '{"version":1,"records":[{}]}',
        ]) {
          await file.writeAsString(content);
          await expectLater(journal.read(), throwsA(anything));
          expect(await file.readAsString(), content);
        }
      },
    );

    test('symlink journal cannot redirect a write', () async {
      await journal.read();
      final target = File('${root.path}/unrelated')..writeAsStringSync('keep');
      await Link('${root.path}/recovery/settings.json').create(target.path);
      await expectLater(journal.read(), throwsA(isA<FileSystemException>()));
      expect(await target.readAsString(), 'keep');
    });

    test(
      'another owner is refused while the original owner is alive',
      () async {
        await journal.read();
        final other = FileProxyJournal(
          () async => Directory('${root.path}/recovery'),
        );
        addTearDown(other.close);
        await expectLater(other.read(), throwsA(isA<FileSystemException>()));
      },
    );

    test(
      'SIGKILL releases the OS lock and a new owner recovers the durable record',
      () async {
        final rootPackage = File(
          'plugins/proxy/test/fixtures/journal_owner.dart',
        );
        final fixture = rootPackage.existsSync()
            ? rootPackage
            : File('test/fixtures/journal_owner.dart');
        final child = await Process.start('dart', [
          fixture.absolute.path,
          '${root.path}/recovery',
        ]);
        final errors = child.stderr.transform(utf8.decoder).join();
        try {
          final ready = await child.stdout
              .transform(utf8.decoder)
              .transform(const LineSplitter())
              .first
              .timeout(const Duration(seconds: 15));
          expect(ready, 'READY');
          await expectLater(
            journal.read(),
            throwsA(isA<FileSystemException>()),
          );
          expect(child.kill(ProcessSignal.sigkill), isTrue);
          expect(await child.exitCode, isNot(0));
          expect(await errors, isEmpty);
          final host = _Host()
            ..values['webproxy'] = endpoint('127.0.0.1', 7890);
          final replacement = MacosProxy(
            journal: journal,
            commandRunner: ProxyCommandRunner(host.run),
          );
          expect(await replacement.stop(), isTrue);
          expect(host.values['webproxy'], endpoint('old', 8888));
          expect(await journal.read(), isEmpty);
        } finally {
          child.kill(ProcessSignal.sigkill);
          await child.exitCode;
        }
      },
      timeout: const Timeout(Duration(seconds: 30)),
    );
  }, skip: Platform.isWindows);
}

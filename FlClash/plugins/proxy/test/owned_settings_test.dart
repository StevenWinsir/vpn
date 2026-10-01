import 'dart:async';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:proxy/src/macos_proxy.dart';
import 'package:proxy/src/owned_settings.dart';
import 'package:proxy/src/proxy_command.dart';

void main() {
  test(
    'restores original fields and never overwrites another application',
    () async {
      final owner = OwnedProxySettings();
      final values = {'server': 'old:8888', 'mode': 'auto'};
      ProxySetting setting(String key, String target) => ProxySetting(
        key: key,
        target: target,
        read: () async => values[key],
        write: (value) async {
          values[key] = value;
          return true;
        },
      );
      expect(await owner.restore(), isTrue);
      expect(
        await owner.apply([
          setting('server', '127.0.0.1:7890'),
          setting('mode', 'manual'),
        ]),
        isTrue,
      );
      values['server'] = 'other-app:9000';
      expect(await owner.restore(), isTrue);
      expect(values, {'server': 'other-app:9000', 'mode': 'auto'});
    },
  );

  test(
    'reconfiguration preserves the first baseline and rolls back partial failure',
    () async {
      final owner = OwnedProxySettings();
      var value = 'original';
      ProxySetting setting(String target, {bool fail = false}) => ProxySetting(
        key: 'server',
        target: target,
        read: () async => value,
        write: (next) async {
          value = next;
          return !(fail && next == target);
        },
      );
      expect(await owner.apply([setting('first')]), isTrue);
      expect(await owner.apply([setting('second')]), isTrue);
      expect(await owner.restore(), isTrue);
      expect(value, 'original');
      expect(await owner.apply([setting('partial', fail: true)]), isFalse);
      expect(value, 'original');
    },
  );

  test('failed restore retains ownership for a bounded later retry', () async {
    final owner = OwnedProxySettings();
    var value = 'original', fail = false;
    expect(
      await owner.apply([
        ProxySetting(
          key: 'mode',
          target: 'managed',
          read: () async => value,
          write: (next) async {
            if (fail) return false;
            value = next;
            return true;
          },
        ),
      ]),
      isTrue,
    );
    fail = true;
    expect(await owner.restore(), isFalse);
    fail = false;
    expect(await owner.restore(), isTrue);
    expect(value, 'original');
  });

  test(
    'stop supersedes a delayed macOS service discovery without OS writes',
    () async {
      final discovered = Completer<ProcessResult>();
      final proxy = MacosProxy(
        commandRunner: ProxyCommandRunner((
          executable,
          args, {
          runInShell = false,
        }) async {
          if (args.first == '-listallnetworkservices') return discovered.future;
          if (args.first == '-getautoproxyurl')
            return ProcessResult(1, 0, 'URL: (null)\nEnabled: No\n', '');
          fail('a stale discovery must not write system settings');
        }),
      );
      final starting = proxy.start(7890, []);
      expect(await proxy.stop(), isTrue);
      discovered.complete(ProcessResult(1, 0, 'Wi-Fi\n', ''));
      expect(await starting, isFalse);
    },
  );

  test(
    'macOS restores the prior proxy and preserves external endpoint ownership',
    () async {
      final host = _MacSettings();
      final proxy = MacosProxy(commandRunner: ProxyCommandRunner(host.run));
      expect(await proxy.start(7890, ['localhost']), isTrue);
      host.endpoints['webproxy'] = 'external:9999';
      expect(await proxy.stop(), isTrue);
      expect(host.endpoints['webproxy'], 'external:9999');
      expect(host.endpoints['securewebproxy'], 'old:8888');
      expect(host.endpoints['socksfirewallproxy'], 'old:8888');
      expect(host.bypass, ['old.example']);
      final calls = host.writes;
      expect(await proxy.stop(), isTrue);
      expect(host.writes, calls);
    },
  );

  test(
    'macOS does not replace active PAC or authenticated proxy settings',
    () async {
      final host = _MacSettings()..pac = true;
      final proxy = MacosProxy(commandRunner: ProxyCommandRunner(host.run));
      expect(await proxy.start(7890, []), isFalse);
      expect(host.writes, 0);
      host.pac = false;
      host.authenticated = true;
      expect(await proxy.start(7890, []), isFalse);
      expect(host.writes, 0);
    },
  );
}

class _MacSettings {
  final endpoints = {
    for (final kind in ['webproxy', 'securewebproxy', 'socksfirewallproxy'])
      kind: 'old:8888',
  };
  final enabled = {
    for (final kind in ['webproxy', 'securewebproxy', 'socksfirewallproxy'])
      kind: true,
  };
  List<String> bypass = ['old.example'];
  bool pac = false, authenticated = false;
  int writes = 0;

  Future<ProcessResult> run(
    String executable,
    List<String> args, {
    bool runInShell = false,
  }) async {
    expect(executable, '/usr/sbin/networksetup');
    final option = args.first;
    String output = '';
    if (option == '-listallnetworkservices') {
      output = 'Wi-Fi\n';
    } else if (option == '-getautoproxyurl') {
      output = 'URL: (null)\nEnabled: ${pac ? 'Yes' : 'No'}\n';
    } else if (option == '-getproxybypassdomains') {
      output = bypass.join('\n');
    } else if (option == '-setproxybypassdomains') {
      writes++;
      bypass = args.sublist(2).where((v) => v != 'Empty').toList();
    } else if (option.startsWith('-get')) {
      final kind = option.substring(4),
          endpoint = endpoints[option.substring(4)]!.split(':');
      output =
          'Enabled: ${enabled[kind]! ? 'Yes' : 'No'}\nServer: ${endpoint[0]}\nPort: ${endpoint[1]}\nAuthenticated Proxy Enabled: ${authenticated ? 1 : 0}\n';
    } else if (option.startsWith('-set') && option.endsWith('state')) {
      writes++;
      enabled[option.substring(4, option.length - 5)] = args.last == 'on';
    } else if (option.startsWith('-set')) {
      writes++;
      endpoints[option.substring(4)] = '${args[2]}:${args[3]}';
    } else {
      fail('unexpected command $option');
    }
    return ProcessResult(1, 0, output, '');
  }
}

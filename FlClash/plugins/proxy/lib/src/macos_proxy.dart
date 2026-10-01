import 'dart:io';
import 'dart:convert';

import 'proxy_command.dart';
import 'owned_settings.dart';

class MacosProxy {
  final ProxyCommandRunner _commandRunner;
  final _ownership = OwnedProxySettings();
  int _revision = 0;

  MacosProxy({required ProxyCommandRunner commandRunner})
    : _commandRunner = commandRunner;

  Future<bool> start(int port, List<String> bypassDomain) async {
    final revision = ++_revision;
    final services = await _networkServices();
    final settings = <ProxySetting>[];
    for (final service in services) {
      final pac = await _read('-getautoproxyurl', service);
      if (pac == null ||
          !RegExp(r'^Enabled: No$', multiLine: true).hasMatch(pac.trim())) {
        return false;
      }
      for (final kind in ['webproxy', 'securewebproxy', 'socksfirewallproxy']) {
        final target = jsonEncode({
          'enabled': true,
          'server': proxyHost,
          'port': port,
          'authenticated': false,
        });
        settings.add(
          ProxySetting(
            key: '$service/$kind',
            target: target,
            read: () async {
              final value = await _read('-get$kind', service);
              if (value == null) return null;
              final fields = <String, String>{};
              for (final line in value.split('\n')) {
                final index = line.indexOf(':');
                if (index > 0) {
                  fields[line.substring(0, index).trim()] = line
                      .substring(index + 1)
                      .trim();
                }
              }
              if (!['Yes', 'No'].contains(fields['Enabled']) ||
                  ![
                    'Yes',
                    'No',
                    '0',
                    '1',
                  ].contains(fields['Authenticated Proxy Enabled']) ||
                  fields['Server'] == null) {
                return null;
              }
              final oldPort = int.tryParse(fields['Port'] ?? '');
              if (oldPort == null || oldPort < 0 || oldPort > 65535) {
                return null;
              }
              return jsonEncode({
                'enabled': fields['Enabled'] == 'Yes',
                'server': fields['Server'],
                'port': oldPort,
                'authenticated': [
                  'Yes',
                  '1',
                ].contains(fields['Authenticated Proxy Enabled']),
              });
            },
            acceptOriginal: (value) =>
                (jsonDecode(value) as Map)['authenticated'] == false,
            equivalent: (actual, expected) {
              final a = jsonDecode(actual) as Map,
                  e = jsonDecode(expected) as Map;
              if (e['enabled'] == false &&
                  (e['server'] == '' || e['port'] == 0)) {
                return a['enabled'] == false && a['authenticated'] == false;
              }
              return actual == expected;
            },
            intermediate: (_, value) => [
              jsonEncode({
                ...jsonDecode(value) as Map<String, dynamic>,
                'enabled': false,
              }),
            ],
            write: (value) async {
              final endpoint = jsonDecode(value) as Map;
              return _commandRunner.run([
                if (endpoint['server'] != '' && (endpoint['port'] as int) > 0)
                  ProxyCommand('/usr/sbin/networksetup', [
                    '-set$kind',
                    service,
                    endpoint['server'] as String,
                    '${endpoint['port']}',
                  ]),
                ProxyCommand('/usr/sbin/networksetup', [
                  '-set${kind}state',
                  service,
                  endpoint['enabled'] == true ? 'on' : 'off',
                ]),
              ]);
            },
          ),
        );
      }
      settings.add(
        ProxySetting(
          key: '$service/bypass',
          target: jsonEncode(bypassDomain),
          read: () async {
            final value = await _read('-getproxybypassdomains', service);
            if (value == null) return null;
            return jsonEncode(
              value.startsWith("There aren't any bypass domains set")
                  ? <String>[]
                  : value
                        .split('\n')
                        .map((v) => v.trim())
                        .where((v) => v.isNotEmpty)
                        .toList(),
            );
          },
          write: (value) => _commandRunner.run([
            MacosProxyCommands.buildProxyBypass(
              service,
              (jsonDecode(value) as List).cast<String>(),
            ),
          ]),
        ),
      );
    }
    if (revision != _revision) return false;
    return _ownership.apply(settings);
  }

  Future<bool> stop() {
    _revision++;
    return _ownership.restore();
  }

  Future<String?> _read(String option, String service) async {
    try {
      final result = await _commandRunner.process('/usr/sbin/networksetup', [
        option,
        service,
      ]);
      return result.exitCode == 0
          ? result.stdout.toString().replaceAll('\r', '')
          : null;
    } catch (_) {
      return null;
    }
  }

  Future<List<String>> _networkServices() async {
    try {
      final result = await _commandRunner.process('/usr/sbin/networksetup', [
        '-listallnetworkservices',
      ]);
      if (result.exitCode != 0) {
        return [];
      }
      return MacosProxyCommands.parseNetworkServices(result.stdout.toString());
    } on ProcessException {
      return [];
    }
  }
}

class MacosProxyCommands {
  static List<ProxyCommand> buildStart(
    String service,
    int port,
    List<String> bypassDomain,
  ) {
    return [
      ProxyCommand('/usr/sbin/networksetup', [
        '-setwebproxy',
        service,
        proxyHost,
        '$port',
      ]),
      ProxyCommand('/usr/sbin/networksetup', [
        '-setsecurewebproxy',
        service,
        proxyHost,
        '$port',
      ]),
      ProxyCommand('/usr/sbin/networksetup', [
        '-setsocksfirewallproxy',
        service,
        proxyHost,
        '$port',
      ]),
      buildProxyBypass(service, bypassDomain),
      ProxyCommand('/usr/sbin/networksetup', [
        '-setwebproxystate',
        service,
        'on',
      ]),
      ProxyCommand('/usr/sbin/networksetup', [
        '-setsecurewebproxystate',
        service,
        'on',
      ]),
      ProxyCommand('/usr/sbin/networksetup', [
        '-setsocksfirewallproxystate',
        service,
        'on',
      ]),
    ];
  }

  static List<ProxyCommand> buildStop(String service) {
    return [
      ProxyCommand('/usr/sbin/networksetup', [
        '-setautoproxystate',
        service,
        'off',
      ]),
      ProxyCommand('/usr/sbin/networksetup', [
        '-setwebproxystate',
        service,
        'off',
      ]),
      ProxyCommand('/usr/sbin/networksetup', [
        '-setsecurewebproxystate',
        service,
        'off',
      ]),
      ProxyCommand('/usr/sbin/networksetup', [
        '-setsocksfirewallproxystate',
        service,
        'off',
      ]),
      buildProxyBypass(service, const []),
    ];
  }

  static ProxyCommand buildProxyBypass(
    String service,
    List<String> bypassDomain,
  ) {
    return ProxyCommand('/usr/sbin/networksetup', [
      '-setproxybypassdomains',
      service,
      if (bypassDomain.isEmpty) 'Empty' else ...bypassDomain,
    ]);
  }

  static List<String> parseNetworkServices(String stdout) {
    return stdout
        .split('\n')
        .map((line) => line.trim())
        .where((line) => line.isNotEmpty)
        .where((line) => !line.startsWith('*'))
        .where((line) => !line.startsWith('An asterisk '))
        .toList();
  }
}

import 'dart:io';

import 'package:setup_hooks/src/options.dart';
import 'package:test/test.dart';

void main() {
  test('managed endpoint is validated and affects the build fingerprint', () {
    final root = Directory.systemTemp.createTempSync('managed_api_test_');
    addTearDown(() => root.deleteSync(recursive: true));
    final defaults = BuildConfig.load(rootDir: root.path, environment: {});
    final local = BuildConfig.load(
      rootDir: root.path,
      environment: {'CLIENT_API_BASE': 'http://127.0.0.1:8080/api/v1/client'},
    );
    expect(
      local.goLdflags,
      contains('-X core/managed.APIBase=http://127.0.0.1:8080/api/v1/client'),
    );
    expect(local.toFingerprintMap(), isNot(defaults.toFingerprintMap()));
    expect(
      managedAPIFlags('-w', 'https://vpn.example.invalid/api/v1/client'),
      contains('https://vpn.example.invalid'),
    );
  });

  test('filtered hook environment loads the explicit local client .env', () {
    final root = Directory.systemTemp.createTempSync('managed_api_dotenv_');
    addTearDown(() => root.deleteSync(recursive: true));
    final file = File('${root.path}/.env');
    file.writeAsStringSync(
      '# Local build only\nCLIENT_API_BASE="http://127.0.0.1:8080/api/v1/client"\n',
    );
    final local = BuildConfig.load(rootDir: root.path, environment: {});
    expect(
      local.goLdflags,
      contains('-X core/managed.APIBase=http://127.0.0.1:8080/api/v1/client'),
    );
    file.writeAsStringSync(
      'CLIENT_API_BASE=https://other.example.invalid/api/v1/client\n',
    );
    expect(
      BuildConfig.load(rootDir: root.path, environment: {}).toFingerprintMap(),
      isNot(local.toFingerprintMap()),
    );
    final explicit = BuildConfig.load(
      rootDir: root.path,
      environment: {
        'CLIENT_API_BASE': 'https://ci.example.invalid/api/v1/client',
      },
    );
    expect(explicit.goLdflags, contains('https://ci.example.invalid'));
  });

  test('client .env rejects duplicate, empty, unknown or insecure settings', () {
    final root = Directory.systemTemp.createTempSync(
      'managed_api_dotenv_invalid_',
    );
    addTearDown(() => root.deleteSync(recursive: true));
    final file = File('${root.path}/.env');
    for (final value in [
      'CLIENT_API_BASE=\n',
      'OTHER_SETTING=secret\n',
      'CLIENT_API_BASE=http://remote.example.invalid/api/v1/client\n',
      'CLIENT_API_BASE=https://example.invalid/api/v1/client\nCLIENT_API_BASE=https://other.example.invalid/api/v1/client\n',
      'CLIENT_API_BASE=https://example.invalid/api/v1/client -X injected=bad\n',
    ]) {
      file.writeAsStringSync(value);
      expect(
        () => BuildConfig.load(rootDir: root.path, environment: {}),
        throwsArgumentError,
      );
    }
    file.deleteSync();
    final target = File('${root.path}/redirect')
      ..writeAsStringSync(
        'CLIENT_API_BASE=https://example.invalid/api/v1/client',
      );
    Link(file.path).createSync(target.path);
    expect(
      () => BuildConfig.load(rootDir: root.path, environment: {}),
      throwsArgumentError,
    );
  });

  test(
    'managed endpoint cannot downgrade remote TLS or inject build flags',
    () {
      for (final value in [
        'http://vpn.example.invalid/api/v1/client',
        'http://localhost/api/v1/client',
        'https://user:password@vpn.example.invalid/api/v1/client',
        'https://vpn.example.invalid/api/v1/client?token=private',
        'https://vpn.example.invalid/api/v1/client#fragment',
        'https://vpn.example.invalid/api/v1/client -X other=value',
        'https://vpn.example.invalid/api/v1',
        'https://vpn.example.invalid/api/v1/client\n',
      ]) {
        expect(() => managedAPIFlags('-w', value), throwsArgumentError);
      }
    },
  );
}

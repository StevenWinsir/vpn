import 'dart:io';

import 'package:logging/logging.dart';
import 'package:path/path.dart' as p;
import 'package:yaml/yaml.dart';

final _log = Logger('options');

class BuildConfig {
  const BuildConfig({
    required this.tags,
    required this.goLdflags,
    required this.coreDir,
    required this.coreName,
    required this.libName,
    required this.outputDir,
    required this.helperDir,
    required this.helperName,
  });

  final String tags;
  final String goLdflags;
  final String coreDir;
  final String coreName;
  final String libName;
  final String outputDir;
  final String helperDir;
  final String helperName;

  static const _defaults = BuildConfig(
    tags: 'with_gvisor',
    goLdflags: '-w -s',
    coreDir: 'core',
    coreName: 'FlClashCore',
    libName: 'libclash',
    outputDir: 'libclash',
    helperDir: 'services/helper',
    helperName: 'FlClashHelperService',
  );

  static BuildConfig load({
    required String rootDir,
    Map<String, String>? environment,
  }) {
    final file = File(p.join(rootDir, 'build_config.yaml'));
    final yaml = file.existsSync()
        ? loadYaml(file.readAsStringSync()) as YamlMap?
        : null;
    if (yaml == null) _log.fine('Using default build configuration');
    return BuildConfig(
      tags: yaml?['tags'] as String? ?? _defaults.tags,
      goLdflags: managedAPIFlags(
        yaml?['go_ldflags'] as String? ?? _defaults.goLdflags,
        (environment ?? Platform.environment)['CLIENT_API_BASE'],
      ),
      coreDir: yaml?['core_dir'] as String? ?? _defaults.coreDir,
      coreName: yaml?['core_name'] as String? ?? _defaults.coreName,
      libName: yaml?['lib_name'] as String? ?? _defaults.libName,
      outputDir: yaml?['output_dir'] as String? ?? _defaults.outputDir,
      helperDir: yaml?['helper_dir'] as String? ?? _defaults.helperDir,
      helperName: yaml?['helper_name'] as String? ?? _defaults.helperName,
    );
  }

  Map<String, String> toFingerprintMap() => {
    'tags': tags,
    'go_ldflags': goLdflags,
    'core_dir': coreDir,
    'core_name': coreName,
    'lib_name': libName,
    'output_dir': outputDir,
    'helper_dir': helperDir,
    'helper_name': helperName,
  };
}

String managedAPIFlags(String flags, String? endpoint) {
  if (endpoint == null || endpoint.isEmpty) return flags;
  final uri = Uri.tryParse(endpoint);
  if (uri == null ||
      endpoint.trim() != endpoint ||
      !RegExp(
        r'^https?://[a-zA-Z0-9.\-:\[\]]+/api/v1/client$',
      ).hasMatch(endpoint) ||
      uri.host.isEmpty ||
      (uri.hasPort && (uri.port < 1 || uri.port > 65535)) ||
      uri.hasQuery ||
      uri.hasFragment ||
      uri.userInfo.isNotEmpty ||
      (uri.scheme != 'https' &&
          !(uri.scheme == 'http' && uri.host == '127.0.0.1'))) {
    throw ArgumentError(
      'CLIENT_API_BASE must use HTTPS (HTTP only for 127.0.0.1) and end in /api/v1/client',
    );
  }
  return '$flags -X core/managed.APIBase=$endpoint';
}

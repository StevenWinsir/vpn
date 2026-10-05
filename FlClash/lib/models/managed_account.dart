import 'managed_configuration.dart';

bool isManagedCredentialScope(String value) {
  final uri = Uri.tryParse(value);
  return value.length <= 2048 &&
      value == value.trim() &&
      uri != null &&
      uri.host.isNotEmpty &&
      uri.userInfo.isEmpty &&
      !uri.hasQuery &&
      !uri.hasFragment &&
      uri.path == '/api/v1/client' &&
      (!uri.hasPort || uri.port > 0 && uri.port <= 65535) &&
      (uri.scheme == 'https' ||
          uri.scheme == 'http' &&
              const {'127.0.0.1', '::1'}.contains(uri.host));
}

enum ManagedPhase {
  signedOut('signed_out'),
  authenticating('authenticating'),
  profileRequired('profile_required'),
  loadingConfiguration('loading_configuration'),
  configurationStaged('configuration_staged'),
  configurationApplied('configuration_applied'),
  restricted('restricted'),
  unavailable('unavailable');

  const ManagedPhase(this.wireName);

  final String wireName;
}

class ManagedLoginRequest {
  const ManagedLoginRequest({
    required this.email,
    required this.password,
    required this.deviceId,
    required this.platform,
    required this.appVersion,
  });

  final String email;
  final String password;
  final String deviceId;
  final String platform;
  final String appVersion;

  Map<String, Object?> toJson() => {
    'email': email,
    'password': password,
    'device_id': deviceId,
    'platform': platform,
    'app_version': appVersion,
  };

  @override
  String toString() => 'ManagedLoginRequest(<redacted>)';
}

class ManagedUser {
  const ManagedUser({
    required this.id,
    required this.email,
    required this.name,
  });

  final String id;
  final String email;
  final String name;

  factory ManagedUser.fromJson(Map<String, dynamic> value) => ManagedUser(
    id: value['id'] as String,
    email: value['email'] as String,
    name: value['name'] as String,
  );
}

class ManagedSession {
  const ManagedSession({
    required this.id,
    required this.expiresAt,
    required this.planName,
    required this.remainingBytes,
    required this.totalBytes,
    required this.reason,
    required this.subscriptionExpiresAt,
    this.ratePermille = 1000,
  });

  final String id;
  final DateTime expiresAt;
  final String planName;
  final int remainingBytes;
  final int totalBytes;
  final String reason;
  final DateTime? subscriptionExpiresAt;
  final int ratePermille;

  factory ManagedSession.fromJson(Map<String, dynamic> value) {
    final remaining = value['remaining_bytes'] as int;
    final total = value['total_bytes'] as int;
    if (remaining < 0 ||
        total < remaining ||
        total > (1 << 50) ||
        value['session_idle_timeout_seconds'] != 180 ||
        value['report_interval_seconds'] != 60 ||
        value['lease_seconds'] != 90 ||
        value['metering_source'] != 'client_reported' ||
        value['rate_permille'] is! int ||
        (value['rate_permille'] as int) < 1 ||
        (value['rate_permille'] as int) > 10000) {
      throw const FormatException('Invalid managed session');
    }
    return ManagedSession(
      id: value['session_id'] as String,
      expiresAt: DateTime.parse(value['expires_at'] as String),
      planName: value['plan_name'] as String,
      remainingBytes: remaining,
      totalBytes: total,
      reason: value['reason'] as String,
      ratePermille: value['rate_permille'] as int,
      subscriptionExpiresAt: value['subscription_expires_at'] == null
          ? null
          : DateTime.parse(value['subscription_expires_at'] as String),
    );
  }
}

class ManagedMetering {
  const ManagedMetering({
    required this.sequence,
    required this.acknowledgedUpload,
    required this.acknowledgedDownload,
    required this.upload,
    required this.download,
    required this.confirmedRemaining,
    required this.estimatedRemaining,
    required this.pending,
  });

  final int sequence;
  final int acknowledgedUpload;
  final int acknowledgedDownload;
  final int upload;
  final int download;
  final int confirmedRemaining;
  final int estimatedRemaining;
  final bool pending;

  factory ManagedMetering.fromJson(Map<String, dynamic> value) {
    int counter(String key) {
      final number = value[key];
      if (number is! int || number < 0 || number > (1 << 50)) {
        throw const FormatException('Invalid cumulative counter');
      }
      return number;
    }

    final result = ManagedMetering(
      sequence: counter('sequence'),
      acknowledgedUpload: counter('acknowledged_upload_bytes'),
      acknowledgedDownload: counter('acknowledged_download_bytes'),
      upload: counter('upload_bytes'),
      download: counter('download_bytes'),
      confirmedRemaining: counter('confirmed_remaining_bytes'),
      estimatedRemaining: counter('estimated_remaining_bytes'),
      pending: value['pending'] as bool,
    );
    if (result.sequence > (1 << 40) ||
        result.acknowledgedUpload > result.upload ||
        result.acknowledgedDownload > result.download ||
        result.estimatedRemaining > result.confirmedRemaining) {
      throw const FormatException('Invalid cumulative baseline');
    }
    return result;
  }
}

class ManagedAccountSnapshot {
  const ManagedAccountSnapshot({
    this.apiBase = '',
    this.generation = 0,
    this.runtimeRevision = 0,
    this.phase = ManagedPhase.signedOut,
    this.busy = false,
    this.user,
    this.session,
    this.profileVersion = '',
    this.configuration,
    this.errorCode = '',
    this.canConnect = false,
    this.metering,
  });

  final String apiBase;
  final int generation;
  final int runtimeRevision;
  final ManagedPhase phase;
  final bool busy;
  final ManagedUser? user;
  final ManagedSession? session;
  final String profileVersion;
  final ManagedConfiguration? configuration;
  final String errorCode;
  final bool canConnect;
  final ManagedMetering? metering;

  factory ManagedAccountSnapshot.fromJson(Map<String, dynamic> value) {
    if (value.containsKey('token') ||
        value.containsKey('yaml') ||
        value.containsKey('password') ||
        value['can_connect'] is! bool) {
      throw const FormatException('Unsupported managed authorization');
    }
    final apiBase = value['api_base'] as String? ?? '';
    if (apiBase.isNotEmpty && !isManagedCredentialScope(apiBase)) {
      throw const FormatException('Invalid managed API scope');
    }
    final generation = value['generation'] as int;
    final runtimeRevision = value['runtime_revision'] as int? ?? 0;
    if (runtimeRevision < 0 || runtimeRevision > (1 << 50)) {
      throw const FormatException('Invalid runtime revision');
    }
    final phase = ManagedPhase.values.firstWhere(
      (phase) => phase.wireName == value['phase'],
    );
    final user = value['user'] == null
        ? null
        : ManagedUser.fromJson(Map<String, dynamic>.from(value['user'] as Map));
    final session = value['session'] == null
        ? null
        : ManagedSession.fromJson(
            Map<String, dynamic>.from(value['session'] as Map),
          );
    if (generation < 0 ||
        (user == null) != (session == null) ||
        (phase == ManagedPhase.signedOut && user != null)) {
      throw const FormatException('Invalid managed account');
    }
    final rawConfiguration = value['configuration'];
    final configuration = rawConfiguration == null
        ? null
        : ManagedConfiguration.fromJson(
            Map<String, dynamic>.from(rawConfiguration as Map),
            generation: generation,
            userId: user?.id ?? '',
            sessionId: session?.id ?? '',
            version: value['profile_version'] as String,
          );
    if ((phase == ManagedPhase.configurationApplied) !=
        (configuration != null)) {
      throw const FormatException('Invalid managed configuration phase');
    }
    final metering = value['metering'] == null
        ? null
        : ManagedMetering.fromJson(
            Map<String, dynamic>.from(value['metering'] as Map),
          );
    final allowed = value['can_connect'] as bool;
    if (allowed &&
        (runtimeRevision == 0 ||
            configuration == null ||
            session == null ||
            metering == null ||
            metering.sequence == 0 ||
            metering.estimatedRemaining == 0 ||
            (value['session'] as Map)['can_connect'] != true)) {
      throw const FormatException('Unconfirmed managed connection');
    }
    return ManagedAccountSnapshot(
      apiBase: apiBase,
      generation: generation,
      runtimeRevision: runtimeRevision,
      phase: phase,
      busy: value['busy'] as bool,
      user: user,
      session: session,
      profileVersion: value['profile_version'] as String,
      configuration: configuration,
      errorCode: value['error_code'] as String,
      canConnect: allowed,
      metering: metering,
    );
  }
}

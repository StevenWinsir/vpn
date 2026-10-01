class ManagedConfigurationGroup {
  const ManagedConfigurationGroup({
    required this.name,
    required this.selected,
    required this.proxies,
  });

  final String name;
  final String selected;
  final List<String> proxies;

  factory ManagedConfigurationGroup.fromJson(Map<String, dynamic> value) {
    _exactKeys(value, {'name', 'type', 'selected', 'proxies'});
    final members = value['proxies'];
    if (!_validName(value['name']) ||
        value['type'] != 'select' ||
        members is! List ||
        members.isEmpty ||
        members.length > 512 ||
        !members.every(_validName) ||
        !members.contains(value['selected'])) {
      throw const FormatException('Invalid managed group');
    }
    return ManagedConfigurationGroup(
      name: value['name'] as String,
      selected: value['selected'] as String,
      proxies: List<String>.unmodifiable(members.cast<String>()),
    );
  }
}

class ManagedConfiguration {
  const ManagedConfiguration({
    required this.id,
    required this.version,
    required this.groups,
  });

  final String id;
  final String version;
  final List<ManagedConfigurationGroup> groups;

  factory ManagedConfiguration.fromJson(
    Map<String, dynamic> value, {
    required int generation,
    required String userId,
    required String sessionId,
    required String version,
  }) {
    _exactKeys(value, {'id', 'version', 'owner', 'groups'});
    final owner = value['owner'];
    final groups = value['groups'];
    if (owner is! Map<String, dynamic> ||
        groups is! List ||
        groups.isEmpty ||
        groups.length > 128 ||
        value['id'] is! String ||
        !RegExp(r'^[a-f0-9]{32}$').hasMatch(value['id'] as String) ||
        !RegExp(r'^[a-f0-9]{64}$').hasMatch(version) ||
        value['version'] != version) {
      throw const FormatException('Invalid managed configuration');
    }
    _exactKeys(owner, {'generation', 'user_id', 'session_id'});
    if (owner['generation'] != generation ||
        owner['user_id'] != userId ||
        owner['session_id'] != sessionId) {
      throw const FormatException('Invalid managed configuration owner');
    }
    final parsed = groups
        .map((group) {
          if (group is! Map<String, dynamic>) {
            throw const FormatException('Invalid managed group');
          }
          return ManagedConfigurationGroup.fromJson(group);
        })
        .toList(growable: false);
    if (parsed.map((group) => group.name).toSet().length != parsed.length) {
      throw const FormatException('Duplicate managed group');
    }
    return ManagedConfiguration(
      id: value['id'] as String,
      version: version,
      groups: List.unmodifiable(parsed),
    );
  }
}

void _exactKeys(Map<String, dynamic> value, Set<String> keys) {
  if (value.length != keys.length || !value.keys.every(keys.contains)) {
    throw const FormatException('Unexpected managed configuration fields');
  }
}

bool _validName(Object? value) =>
    value is String &&
    value.isNotEmpty &&
    value.length <= 256 &&
    !RegExp(r'[\x00-\x1f\x7f]').hasMatch(value);

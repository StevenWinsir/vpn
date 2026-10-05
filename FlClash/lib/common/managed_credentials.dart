import 'dart:convert';
import 'dart:io';

import 'package:fl_clash/models/managed_account.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

class RememberedManagedLogin {
  const RememberedManagedLogin(this.email, this.password);

  final String email;
  final String password;

  bool get valid =>
      email.isNotEmpty &&
      email == email.trim() &&
      email.contains('@') &&
      utf8.encode(email).length <= 254 &&
      password.isNotEmpty &&
      utf8.encode(password).length <= 72;

  @override
  String toString() => 'RememberedManagedLogin(<redacted>)';
}

class ManagedCredentialException implements Exception {
  const ManagedCredentialException();

  @override
  String toString() => 'macOS credential storage unavailable';
}

abstract interface class ManagedCredentialStore {
  bool get supported;
  Future<RememberedManagedLogin?> read(String scope);
  Future<void> save(String scope, RememberedManagedLogin login);
  Future<void> delete(String scope);
}

ManagedCredentialStore? _testCredentialStore;

ManagedCredentialStore createManagedCredentialStore() =>
    _testCredentialStore ?? MacOSManagedCredentials();

@visibleForTesting
void setManagedCredentialStoreForTesting(ManagedCredentialStore? store) {
  assert(() {
    _testCredentialStore = store;
    return true;
  }());
}

class MacOSManagedCredentials implements ManagedCredentialStore {
  MacOSManagedCredentials({
    MethodChannel channel = const MethodChannel(
      'asterlink/managed_credentials',
    ),
    bool? platformSupported,
  }) : _channel = channel,
       supported = platformSupported ?? Platform.isMacOS;

  final MethodChannel _channel;
  @override
  final bool supported;
  Future<void> _tail = Future<void>.value();

  Future<T> _enqueue<T>(String scope, Future<T> Function() operation) {
    if (!supported || !isManagedCredentialScope(scope)) {
      return Future<T>.error(const ManagedCredentialException());
    }
    final result = _tail.then((_) async {
      try {
        return await operation();
      } catch (_) {
        throw const ManagedCredentialException();
      }
    });
    _tail = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }

  @override
  Future<RememberedManagedLogin?> read(String scope) =>
      _enqueue(scope, () async {
        final value = await _channel.invokeMapMethod<String, Object?>('read', {
          'scope': scope,
        });
        if (value == null) return null;
        if (value.keys.length != 2 ||
            value['email'] is! String ||
            value['password'] is! String) {
          throw const ManagedCredentialException();
        }
        final login = RememberedManagedLogin(
          value['email']! as String,
          value['password']! as String,
        );
        if (!login.valid) throw const ManagedCredentialException();
        return login;
      });

  @override
  Future<void> save(String scope, RememberedManagedLogin login) =>
      _enqueue(scope, () async {
        if (!login.valid) throw const ManagedCredentialException();
        if (await _channel.invokeMethod<bool>('save', {
              'scope': scope,
              'email': login.email,
              'password': login.password,
            }) !=
            true) {
          throw const ManagedCredentialException();
        }
      });

  @override
  Future<void> delete(String scope) => _enqueue(scope, () async {
    if (await _channel.invokeMethod<bool>('delete', {'scope': scope}) != true) {
      throw const ManagedCredentialException();
    }
  });
}

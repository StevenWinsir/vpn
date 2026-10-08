// GENERATED CODE - DO NOT MODIFY BY HAND

part of '../managed_account.dart';

// **************************************************************************
// RiverpodGenerator
// **************************************************************************

// GENERATED CODE - DO NOT MODIFY BY HAND
// ignore_for_file: type=lint, type=warning

@ProviderFor(managedInstallationId)
final managedInstallationIdProvider = ManagedInstallationIdProvider._();

final class ManagedInstallationIdProvider
    extends $FunctionalProvider<AsyncValue<String>, String, FutureOr<String>>
    with $FutureModifier<String>, $FutureProvider<String> {
  ManagedInstallationIdProvider._()
    : super(
        from: null,
        argument: null,
        retry: null,
        name: r'managedInstallationIdProvider',
        isAutoDispose: false,
        dependencies: null,
        $allTransitiveDependencies: null,
      );

  @override
  String debugGetCreateSourceHash() => _$managedInstallationIdHash();

  @$internal
  @override
  $FutureProviderElement<String> $createElement($ProviderPointer pointer) =>
      $FutureProviderElement(pointer);

  @override
  FutureOr<String> create(Ref ref) {
    return managedInstallationId(ref);
  }
}

String _$managedInstallationIdHash() =>
    r'ec797a0b74d7e15454ad57ddd4208664b4f120c4';

@ProviderFor(ManagedAccount)
final managedAccountProvider = ManagedAccountProvider._();

final class ManagedAccountProvider
    extends $NotifierProvider<ManagedAccount, ManagedAccountState> {
  ManagedAccountProvider._()
    : super(
        from: null,
        argument: null,
        retry: null,
        name: r'managedAccountProvider',
        isAutoDispose: false,
        dependencies: null,
        $allTransitiveDependencies: null,
      );

  @override
  String debugGetCreateSourceHash() => _$managedAccountHash();

  @$internal
  @override
  ManagedAccount create() => ManagedAccount();

  /// {@macro riverpod.override_with_value}
  Override overrideWithValue(ManagedAccountState value) {
    return $ProviderOverride(
      origin: this,
      providerOverride: $SyncValueProvider<ManagedAccountState>(value),
    );
  }
}

String _$managedAccountHash() => r'c522fcc84315e2e053ad6738d4c4b6d459e6e799';

abstract class _$ManagedAccount extends $Notifier<ManagedAccountState> {
  ManagedAccountState build();
  @$mustCallSuper
  @override
  WhenComplete runBuild() {
    final ref = this.ref as $Ref<ManagedAccountState, ManagedAccountState>;
    final element =
        ref.element
            as $ClassProviderElement<
              AnyNotifier<ManagedAccountState, ManagedAccountState>,
              ManagedAccountState,
              Object?,
              Object?
            >;
    return element.handleCreate(ref, build);
  }
}

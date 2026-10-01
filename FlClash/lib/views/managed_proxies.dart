import 'package:fl_clash/common/common.dart';
import 'package:fl_clash/providers/managed_account.dart';
import 'package:fl_clash/widgets/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:material_ui/material_ui.dart';

class ManagedProxiesView extends ConsumerWidget {
  const ManagedProxiesView({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(managedAccountProvider);
    final configuration = state.account.configuration;
    final strings = context.appLocalizations;
    return CommonScaffold(
      title: strings.proxies,
      body: configuration == null
          ? Center(child: Text(strings.managedNoConfiguration))
          : ListView(
              padding: const EdgeInsets.all(20),
              children: [
                Text(strings.managedSelectHint),
                const SizedBox(height: 16),
                if (state.errorCode.isNotEmpty)
                  Text(strings.managedRequestFailed),
                for (final group in configuration.groups) ...[
                  ListHeader(title: group.name),
                  for (final proxy in group.proxies)
                    ListTile(
                      key: ValueKey('managed-node-${group.name}-$proxy'),
                      title: Text(proxy),
                      selected: group.selected == proxy,
                      trailing: group.selected == proxy
                          ? const Icon(Icons.check_circle_outline)
                          : null,
                      enabled: !state.busy,
                      onTap: () => ref
                          .read(managedAccountProvider.notifier)
                          .selectProxy(group.name, proxy),
                    ),
                  const Divider(),
                ],
              ],
            ),
    );
  }
}

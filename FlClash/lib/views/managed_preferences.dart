import 'package:fl_clash/common/common.dart';
import 'package:fl_clash/l10n/l10n.dart';
import 'package:fl_clash/providers/providers.dart';
import 'package:fl_clash/views/theme.dart';
import 'package:fl_clash/widgets/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:material_ui/material_ui.dart';

class ManagedPreferencesView extends ConsumerWidget {
  const ManagedPreferencesView({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final strings = context.appLocalizations;
    final locale = getLocaleForString(ref.watch(appSettingProvider).locale);
    String label(Locale? value) => value?.label ?? strings.defaultText;
    return CommonScaffold(
      title: strings.tools,
      body: ListView(
        children: [
          ListItem<Locale?>.options(
            leading: const Icon(Icons.language_outlined),
            title: Text(strings.language),
            subtitle: Text(label(locale)),
            dialogTitle: strings.language,
            options: [null, ...AppLocalizations.delegate.supportedLocales],
            onChanged: (value) => ref
                .read(appSettingProvider.notifier)
                .update((state) => state.copyWith(locale: value?.toString())),
            textBuilder: label,
            value: locale,
          ),
          ListItem.open(
            leading: const Icon(Icons.style),
            title: Text(strings.theme),
            subtitle: Text(strings.themeDesc),
            widget: const ThemeView(),
          ),
        ],
      ),
    );
  }
}

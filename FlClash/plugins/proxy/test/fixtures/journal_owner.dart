import 'dart:io';

import 'package:proxy/src/owned_settings.dart';
import 'package:proxy/src/proxy_journal.dart';

Future<void> main(List<String> arguments) async {
  final journal = FileProxyJournal(() async => Directory(arguments.single));
  await journal.write(const [
    OwnedProxyRecord(
      'Wi-Fi/webproxy',
      '{"enabled":true,"server":"old","port":8888,"authenticated":false}',
      [
        '{"enabled":true,"server":"127.0.0.1","port":7890,"authenticated":false}',
      ],
    ),
  ]);
  stdout.writeln('READY');
  await Future<void>.delayed(const Duration(minutes: 1));
  await journal.close();
}

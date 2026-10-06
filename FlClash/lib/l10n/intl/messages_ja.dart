// DO NOT EDIT. This is code generated via package:intl/generate_localized.dart
// This is a library that provides messages for a ja locale. All the
// messages from the main program should be duplicated here with the same
// function name.

// Ignore issues from commonly used lints in this file.
// ignore_for_file:unnecessary_brace_in_string_interps, unnecessary_new
// ignore_for_file:prefer_single_quotes,comment_references, directives_ordering
// ignore_for_file:annotate_overrides,prefer_generic_function_type_aliases
// ignore_for_file:unused_import, file_names, avoid_escaping_inner_quotes
// ignore_for_file:unnecessary_string_interpolations, unnecessary_string_escapes

import 'package:intl/intl.dart';
import 'package:intl/message_lookup_by_library.dart';

final messages = new MessageLookup();

typedef String MessageIfAbsent(String messageStr, List<dynamic> args);

class MessageLookup extends MessageLookupByLibrary {
  String get localeName => 'ja';

  static String m0(code) =>
      "Windows が FlClashCore.exe の実行を拒否しました（エラー ${code}）。スマート アプリ コントロールや AppLocker などのアプリ制御ポリシーは未署名のプログラムをブロックします。ポリシーで FlClash を許可するか、ポリシーを無効にしてから再試行してください。";

  static String m1(name) =>
      "アプリの起動が2回連続で完了しませんでした。クラッシュループを断ち切るため、プロファイル ${name} の選択を解除し、今回の自動セットアップをスキップしました。いつでも選択し直せます。";

  static String m2(url) => "${url} からプロファイルを作成しますか？";

  static String m3(count) => "${count} 日前";

  static String m4(label) => "選択した${label}を削除してもよろしいですか？";

  static String m5(label) => "この${label}を削除してもよろしいですか？";

  static String m6(label) => "${label}の詳細";

  static String m7(label) => "${label}は空にできません";

  static String m8(count) => "${count} 件";

  static String m9(label) => "${label}はすでに存在します";

  static String m10(name) => "${name} はすでに最新です";

  static String m11(name) => "${name} を更新しました";

  static String m12(count) => "${count} 時間前";

  static String m13(count) => "${count} 時間";

  static String m14(target) => "${target} は無効なポリシーです";

  static String m15(proxyName) => "${proxyName} は無効なプロキシです";

  static String m16(providerName) => "${providerName} は無効なプロキシプロバイダーです";

  static String m17(subRule) => "${subRule} は無効な SUB_RULE です";

  static String m18(appName) =>
      "1. システム設定 > プライバシーとセキュリティ を開く\n2. 位置情報サービス を選択\n3. リストで ${appName} を見つけてチェックを入れる\n\n設定が完了したらアプリに戻ると、通常どおり使用できます。ご協力ありがとうございます。";

  static String m19(remaining, total) => "確認済み残量：${remaining} / ${total} MiB";

  static String m20(upload, download) =>
      "セッション累計：アップロード ${upload} MiB / ダウンロード ${download} MiB";

  static String m21(remaining) => "ローカル推定残量：${remaining} MiB（未確認の通信量を差し引いた値）";

  static String m22(date) => "プランの有効期限：${date}";

  static String m23(plan) => "プラン：${plan}";

  static String m24(label, max) => "${label}は最大${max}文字です";

  static String m25(count) => "${count} 分前";

  static String m26(count) => "${count} か月前";

  static String m27(label) => "${label}はまだありません";

  static String m28(label) => "${label}は数値である必要があります";

  static String m29(label) => "${label} は 1024〜49151 の範囲で指定してください";

  static String m30(count) => "プロキシ ${count} 件";

  static String m31(count) => "ルール ${count} 件";

  static String m32(count) => "${count} 秒";

  static String m33(count) => "${count} 件選択中";

  static String m34(label) => "${label}はURLである必要があります";

  static String m35(count) => "${count} 年前";

  final messages = _notInlinedMessages(_notInlinedMessages);
  static Map<String, Function> _notInlinedMessages(_) => <String, Function>{
    "about": MessageLookupByLibrary.simpleMessage("アプリについて"),
    "accessControl": MessageLookupByLibrary.simpleMessage("アクセス制御"),
    "accessControlAllowDesc": MessageLookupByLibrary.simpleMessage(
      "選択したアプリのみVPNを経由します",
    ),
    "accessControlDesc": MessageLookupByLibrary.simpleMessage(
      "プロキシを利用するアプリを設定します",
    ),
    "accessControlDisabledDesc": MessageLookupByLibrary.simpleMessage(
      "アプリアクセス制御は無効です",
    ),
    "accessControlNotAllowDesc": MessageLookupByLibrary.simpleMessage(
      "選択したアプリはVPNから除外されます",
    ),
    "accessControlSettings": MessageLookupByLibrary.simpleMessage("アクセス制御の設定"),
    "account": MessageLookupByLibrary.simpleMessage("アカウント"),
    "action": MessageLookupByLibrary.simpleMessage("アクション"),
    "actionMode": MessageLookupByLibrary.simpleMessage("モード切替"),
    "actionProxy": MessageLookupByLibrary.simpleMessage("システムプロキシ"),
    "actionStart": MessageLookupByLibrary.simpleMessage("開始/停止"),
    "actionTun": MessageLookupByLibrary.simpleMessage("TUN"),
    "actionView": MessageLookupByLibrary.simpleMessage("表示/非表示"),
    "add": MessageLookupByLibrary.simpleMessage("追加"),
    "addProfile": MessageLookupByLibrary.simpleMessage("プロファイルを追加"),
    "addProxies": MessageLookupByLibrary.simpleMessage("プロキシを追加"),
    "addProxyGroup": MessageLookupByLibrary.simpleMessage("プロキシグループを追加"),
    "addProxyProviders": MessageLookupByLibrary.simpleMessage("プロキシプロバイダーを追加"),
    "addRule": MessageLookupByLibrary.simpleMessage("ルールを追加"),
    "addSsid": MessageLookupByLibrary.simpleMessage("SSIDを追加"),
    "addWidget": MessageLookupByLibrary.simpleMessage("ウィジェットを追加"),
    "addedRules": MessageLookupByLibrary.simpleMessage("追加ルール"),
    "additionalParameters": MessageLookupByLibrary.simpleMessage("追加パラメータ"),
    "address": MessageLookupByLibrary.simpleMessage("アドレス"),
    "addressHelp": MessageLookupByLibrary.simpleMessage("WebDAVサーバーのアドレス"),
    "addressTip": MessageLookupByLibrary.simpleMessage(
      "有効なWebDAVアドレスを入力してください",
    ),
    "advancedConfig": MessageLookupByLibrary.simpleMessage("詳細設定"),
    "advancedConfigDesc": MessageLookupByLibrary.simpleMessage("多彩な設定項目を提供します"),
    "agree": MessageLookupByLibrary.simpleMessage("同意する"),
    "allowBypass": MessageLookupByLibrary.simpleMessage("アプリによるVPNバイパスを許可"),
    "allowBypassDesc": MessageLookupByLibrary.simpleMessage(
      "有効にすると、一部のアプリがVPNをバイパスできます",
    ),
    "allowLan": MessageLookupByLibrary.simpleMessage("LANプロキシ"),
    "allowLanDesc": MessageLookupByLibrary.simpleMessage("LAN経由でのプロキシ利用を許可します"),
    "app": MessageLookupByLibrary.simpleMessage("アプリ"),
    "appAccessControl": MessageLookupByLibrary.simpleMessage("アプリアクセス制御"),
    "appendSystemDns": MessageLookupByLibrary.simpleMessage("システムDNSを追加"),
    "appendSystemDnsTip": MessageLookupByLibrary.simpleMessage(
      "設定にシステムDNSを強制的に追加します",
    ),
    "application": MessageLookupByLibrary.simpleMessage("アプリケーション"),
    "applicationDesc": MessageLookupByLibrary.simpleMessage(
      "アプリケーション関連の設定を変更します",
    ),
    "authentication": MessageLookupByLibrary.simpleMessage("認証"),
    "authenticationDesc": MessageLookupByLibrary.simpleMessage(
      "ローカルプロキシポートに認証を要求し、他のアプリによる無断利用を防ぎます",
    ),
    "authenticationSystemProxyDesc": MessageLookupByLibrary.simpleMessage(
      "認証が有効な間は適用されません",
    ),
    "authorize": MessageLookupByLibrary.simpleMessage("許可"),
    "authorized": MessageLookupByLibrary.simpleMessage("許可済み"),
    "auto": MessageLookupByLibrary.simpleMessage("自動"),
    "autoCheckUpdate": MessageLookupByLibrary.simpleMessage("更新の自動チェック"),
    "autoCheckUpdateDesc": MessageLookupByLibrary.simpleMessage(
      "アプリ起動時に更新を自動的にチェックします",
    ),
    "autoCloseConnections": MessageLookupByLibrary.simpleMessage("接続を自動的に閉じる"),
    "autoCloseConnectionsDesc": MessageLookupByLibrary.simpleMessage(
      "ノードの切り替え後、接続を自動的に閉じます",
    ),
    "autoLaunch": MessageLookupByLibrary.simpleMessage("自動起動"),
    "autoLaunchDesc": MessageLookupByLibrary.simpleMessage("システム起動時に自動的に起動します"),
    "autoRun": MessageLookupByLibrary.simpleMessage("自動実行"),
    "autoRunDesc": MessageLookupByLibrary.simpleMessage("アプリを開いたときに自動的に実行します"),
    "autoSetSystemDns": MessageLookupByLibrary.simpleMessage("システムDNSを自動設定"),
    "autoUpdate": MessageLookupByLibrary.simpleMessage("自動更新"),
    "autoUpdateInterval": MessageLookupByLibrary.simpleMessage("自動更新間隔（分）"),
    "back": MessageLookupByLibrary.simpleMessage("戻る"),
    "backup": MessageLookupByLibrary.simpleMessage("バックアップ"),
    "backupAndRestore": MessageLookupByLibrary.simpleMessage("バックアップと復元"),
    "backupAndRestoreDesc": MessageLookupByLibrary.simpleMessage(
      "WebDAVまたはファイルでデータを同期します",
    ),
    "backupSuccess": MessageLookupByLibrary.simpleMessage("バックアップが完了しました"),
    "basicConfig": MessageLookupByLibrary.simpleMessage("基本設定"),
    "basicConfigDesc": MessageLookupByLibrary.simpleMessage("基本設定をグローバルに変更します"),
    "basicInfo": MessageLookupByLibrary.simpleMessage("基本情報"),
    "basicStrategy": MessageLookupByLibrary.simpleMessage("基本ポリシー"),
    "batteryOptimizationDesc": MessageLookupByLibrary.simpleMessage(
      "バックグラウンドでの動作を維持するため、このアプリの電池の最適化を無効にしてください。タップすると設定を開きます。",
    ),
    "batteryOptimizationStatusTip": MessageLookupByLibrary.simpleMessage(
      "システムの制限により、実行中は電池の最適化の状態を正しく取得できません",
    ),
    "bind": MessageLookupByLibrary.simpleMessage("連携"),
    "blacklistMode": MessageLookupByLibrary.simpleMessage("ブラックリストモード"),
    "blockConnection": MessageLookupByLibrary.simpleMessage("接続をブロック"),
    "bypassDomain": MessageLookupByLibrary.simpleMessage("除外ドメイン"),
    "bypassDomainDesc": MessageLookupByLibrary.simpleMessage(
      "システムプロキシが有効な場合のみ適用されます",
    ),
    "cacheCorrupt": MessageLookupByLibrary.simpleMessage(
      "キャッシュが破損しています。クリアしますか？",
    ),
    "cancel": MessageLookupByLibrary.simpleMessage("キャンセル"),
    "cancelSelectAll": MessageLookupByLibrary.simpleMessage("すべて選択解除"),
    "changeProxyFailedTip": MessageLookupByLibrary.simpleMessage(
      "プロキシの切り替えに失敗したため、前回の選択に戻しました",
    ),
    "changelogBreaking": MessageLookupByLibrary.simpleMessage("破壊的変更"),
    "changelogFeatures": MessageLookupByLibrary.simpleMessage("新機能"),
    "changelogFixes": MessageLookupByLibrary.simpleMessage("不具合修正"),
    "changelogPerformance": MessageLookupByLibrary.simpleMessage("パフォーマンス"),
    "changelogReverts": MessageLookupByLibrary.simpleMessage("取り消し"),
    "checkCertificate": MessageLookupByLibrary.simpleMessage("TLS証明書を検証"),
    "checkCertificateDesc": MessageLookupByLibrary.simpleMessage(
      "信頼できない証明書を拒否します。無効にすると、サブスクリプションやバックアップが中間者攻撃にさらされます",
    ),
    "checkUpdate": MessageLookupByLibrary.simpleMessage("更新を確認"),
    "checkUpdateError": MessageLookupByLibrary.simpleMessage("すでに最新バージョンです"),
    "clearData": MessageLookupByLibrary.simpleMessage("データを消去"),
    "clearSearch": MessageLookupByLibrary.simpleMessage("検索をクリア"),
    "clipboardExport": MessageLookupByLibrary.simpleMessage("クリップボードへエクスポート"),
    "clipboardImport": MessageLookupByLibrary.simpleMessage("クリップボードからインポート"),
    "close": MessageLookupByLibrary.simpleMessage("閉じる"),
    "closeConnections": MessageLookupByLibrary.simpleMessage("接続を閉じる"),
    "color": MessageLookupByLibrary.simpleMessage("カラー"),
    "colorSchemes": MessageLookupByLibrary.simpleMessage("カラースキーム"),
    "columns": MessageLookupByLibrary.simpleMessage("列数"),
    "compatible": MessageLookupByLibrary.simpleMessage("互換モード"),
    "configDataDetected": MessageLookupByLibrary.simpleMessage(
      "設定内にデータが見つかりました",
    ),
    "confirm": MessageLookupByLibrary.simpleMessage("OK"),
    "confirmClearAllData": MessageLookupByLibrary.simpleMessage(
      "すべてのデータを消去してもよろしいですか？",
    ),
    "confirmDeleteProxyGroup": MessageLookupByLibrary.simpleMessage(
      "このプロキシグループを削除してもよろしいですか？",
    ),
    "confirmExitWindow": MessageLookupByLibrary.simpleMessage(
      "現在のウィンドウを閉じてもよろしいですか？",
    ),
    "confirmForceCrashCore": MessageLookupByLibrary.simpleMessage(
      "コアを強制クラッシュさせてもよろしいですか？",
    ),
    "confirmOverwriteTip": MessageLookupByLibrary.simpleMessage(
      "確定すると既存のデータを上書きします",
    ),
    "connected": MessageLookupByLibrary.simpleMessage("接続済み"),
    "connecting": MessageLookupByLibrary.simpleMessage("接続中..."),
    "connection": MessageLookupByLibrary.simpleMessage("接続"),
    "connections": MessageLookupByLibrary.simpleMessage("接続"),
    "connectionsDesc": MessageLookupByLibrary.simpleMessage("現在の接続データを表示します"),
    "connectivity": MessageLookupByLibrary.simpleMessage("接続状態："),
    "content": MessageLookupByLibrary.simpleMessage("内容"),
    "contentNotEmpty": MessageLookupByLibrary.simpleMessage("内容は空にできません"),
    "contentScheme": MessageLookupByLibrary.simpleMessage("コンテンツ"),
    "controlGlobalAddedRules": MessageLookupByLibrary.simpleMessage(
      "グローバル追加ルールを管理",
    ),
    "copy": MessageLookupByLibrary.simpleMessage("コピー"),
    "copyEnvVar": MessageLookupByLibrary.simpleMessage("環境変数をコピー"),
    "copyLink": MessageLookupByLibrary.simpleMessage("リンクをコピー"),
    "copySuccess": MessageLookupByLibrary.simpleMessage("コピーしました"),
    "core": MessageLookupByLibrary.simpleMessage("コア"),
    "coreBlockedByPolicyTip": m0,
    "coreBlockedBySmartAppControlTip": MessageLookupByLibrary.simpleMessage(
      "Windows のスマート アプリ コントロールが、署名されていない FlClashCore.exe をブロックしました。Windows セキュリティ → アプリとブラウザーの制御 → スマート アプリ コントロールの設定で「オフ」を選び、FlClash を再起動してください。一度オフにすると、Windows を再インストールしない限り再度オンにはできません。",
    ),
    "coreStatus": MessageLookupByLibrary.simpleMessage("コアの状態"),
    "country": MessageLookupByLibrary.simpleMessage("地域"),
    "crashDetected": MessageLookupByLibrary.simpleMessage("クラッシュを検出しました"),
    "crashDetectedTip": m1,
    "crashTest": MessageLookupByLibrary.simpleMessage("クラッシュテスト"),
    "crashlytics": MessageLookupByLibrary.simpleMessage("クラッシュ分析"),
    "crashlyticsTip": MessageLookupByLibrary.simpleMessage(
      "有効にすると、アプリのクラッシュ時に機密情報を含まないクラッシュログを自動的にアップロードします",
    ),
    "create": MessageLookupByLibrary.simpleMessage("作成"),
    "createProfile": MessageLookupByLibrary.simpleMessage("プロファイルを作成"),
    "createProfileFromUrlTip": m2,
    "creationTime": MessageLookupByLibrary.simpleMessage("作成日時"),
    "custom": MessageLookupByLibrary.simpleMessage("カスタム"),
    "cut": MessageLookupByLibrary.simpleMessage("切り取り"),
    "dark": MessageLookupByLibrary.simpleMessage("ダーク"),
    "dashboard": MessageLookupByLibrary.simpleMessage("ダッシュボード"),
    "dataChangedSave": MessageLookupByLibrary.simpleMessage(
      "データの変更を検出しました。保存しますか？",
    ),
    "dataCollectionContent": MessageLookupByLibrary.simpleMessage(
      "本アプリは、安定性向上のために Firebase Crashlytics を使用してクラッシュ情報を収集します。\n収集されるデータにはデバイス情報とクラッシュの詳細が含まれますが、個人の機密データは含まれません。\nこの機能は設定で無効にできます。",
    ),
    "dataCollectionTip": MessageLookupByLibrary.simpleMessage("データ収集について"),
    "databaseWriteFailedTip": MessageLookupByLibrary.simpleMessage(
      "変更の保存に失敗したため、元に戻しました",
    ),
    "daysAgo": m3,
    "defaultNameserver": MessageLookupByLibrary.simpleMessage("デフォルトネームサーバー"),
    "defaultNameserverDesc": MessageLookupByLibrary.simpleMessage(
      "DNSサーバーの名前解決に使用します",
    ),
    "defaultText": MessageLookupByLibrary.simpleMessage("デフォルト"),
    "delay": MessageLookupByLibrary.simpleMessage("遅延"),
    "delayTest": MessageLookupByLibrary.simpleMessage("遅延テスト"),
    "delete": MessageLookupByLibrary.simpleMessage("削除"),
    "deleteMultipTip": m4,
    "deleteTip": m5,
    "desc": MessageLookupByLibrary.simpleMessage(
      "ClashMetaベースのマルチプラットフォーム対応プロキシクライアント。シンプルで使いやすく、オープンソースで広告もありません。",
    ),
    "destination": MessageLookupByLibrary.simpleMessage("宛先"),
    "destinationGeoIP": MessageLookupByLibrary.simpleMessage("宛先GeoIP"),
    "destinationIPASN": MessageLookupByLibrary.simpleMessage("宛先IP ASN"),
    "details": m6,
    "detectionTip": MessageLookupByLibrary.simpleMessage(
      "サードパーティAPIに依存しているため、参考値です",
    ),
    "developerMode": MessageLookupByLibrary.simpleMessage("開発者モード"),
    "developerModeEnableTip": MessageLookupByLibrary.simpleMessage(
      "開発者モードが有効になりました。",
    ),
    "direct": MessageLookupByLibrary.simpleMessage("ダイレクト"),
    "disableUDP": MessageLookupByLibrary.simpleMessage("UDPを無効化"),
    "disclaimer": MessageLookupByLibrary.simpleMessage("免責事項"),
    "disclaimerDesc": MessageLookupByLibrary.simpleMessage(
      "本ソフトウェアは、学習・交流や研究などの非商用目的でのみ使用できます。商用目的での使用は固く禁じられています。いかなる商業行為も本ソフトウェアとは一切関係ありません。",
    ),
    "disconnected": MessageLookupByLibrary.simpleMessage("切断済み"),
    "discoverNewVersion": MessageLookupByLibrary.simpleMessage(
      "新しいバージョンが見つかりました",
    ),
    "dnsDesc": MessageLookupByLibrary.simpleMessage("DNS関連の設定を更新します"),
    "dnsHijacking": MessageLookupByLibrary.simpleMessage("DNSハイジャック"),
    "dnsMode": MessageLookupByLibrary.simpleMessage("DNSモード"),
    "domain": MessageLookupByLibrary.simpleMessage("ドメイン"),
    "download": MessageLookupByLibrary.simpleMessage("ダウンロード"),
    "edit": MessageLookupByLibrary.simpleMessage("編集"),
    "editGlobalRules": MessageLookupByLibrary.simpleMessage("グローバルルールを編集"),
    "editProxy": MessageLookupByLibrary.simpleMessage("プロキシを編集"),
    "editProxyGroup": MessageLookupByLibrary.simpleMessage("プロキシグループを編集"),
    "editRule": MessageLookupByLibrary.simpleMessage("ルールを編集"),
    "editSsid": MessageLookupByLibrary.simpleMessage("SSIDを編集"),
    "emptyTip": m7,
    "en": MessageLookupByLibrary.simpleMessage("英語"),
    "entries": MessageLookupByLibrary.simpleMessage(" 件"),
    "entriesCount": m8,
    "exclude": MessageLookupByLibrary.simpleMessage("最近のタスクから隠す"),
    "excludeDesc": MessageLookupByLibrary.simpleMessage(
      "バックグラウンド時に、最近のタスクからアプリを隠します",
    ),
    "excludeProxyFilter": MessageLookupByLibrary.simpleMessage("除外ノードフィルター"),
    "excludeSsids": MessageLookupByLibrary.simpleMessage("除外SSID"),
    "excludeSsidsDesc": MessageLookupByLibrary.simpleMessage(
      "除外したSSIDのWi-Fiに接続すると、アプリの実行状態が自動的に切り替わります",
    ),
    "excludeType": MessageLookupByLibrary.simpleMessage("除外タイプ"),
    "existsTip": m9,
    "exit": MessageLookupByLibrary.simpleMessage("終了"),
    "exitFullScreen": MessageLookupByLibrary.simpleMessage("全画面表示を終了"),
    "expand": MessageLookupByLibrary.simpleMessage("標準"),
    "expectedStatus": MessageLookupByLibrary.simpleMessage("期待するステータス"),
    "expireTime": MessageLookupByLibrary.simpleMessage("有効期限"),
    "exportFile": MessageLookupByLibrary.simpleMessage("ファイルをエクスポート"),
    "exportLogs": MessageLookupByLibrary.simpleMessage("ログをエクスポート"),
    "exportSuccess": MessageLookupByLibrary.simpleMessage("エクスポートが完了しました"),
    "expressiveScheme": MessageLookupByLibrary.simpleMessage("エクスプレッシブ"),
    "externalController": MessageLookupByLibrary.simpleMessage("外部コントローラー"),
    "externalControllerDesc": MessageLookupByLibrary.simpleMessage(
      "有効にすると、ポート9090でClashコアを制御できます",
    ),
    "externalFetch": MessageLookupByLibrary.simpleMessage("外部取得"),
    "externalLink": MessageLookupByLibrary.simpleMessage("外部リンク"),
    "fakeipFilter": MessageLookupByLibrary.simpleMessage("Fake-IPフィルター"),
    "fakeipRange": MessageLookupByLibrary.simpleMessage("Fake-IP範囲"),
    "fallback": MessageLookupByLibrary.simpleMessage("フォールバック"),
    "fallbackDesc": MessageLookupByLibrary.simpleMessage("通常は国外のDNSを使用します"),
    "fallbackFilter": MessageLookupByLibrary.simpleMessage("フォールバックフィルター"),
    "fidelityScheme": MessageLookupByLibrary.simpleMessage("フィデリティ"),
    "file": MessageLookupByLibrary.simpleMessage("ファイル"),
    "fileDesc": MessageLookupByLibrary.simpleMessage("プロファイルファイルを直接アップロードします"),
    "fileIsUpdate": MessageLookupByLibrary.simpleMessage(
      "ファイルが変更されています。変更を保存しますか？",
    ),
    "findProcessMode": MessageLookupByLibrary.simpleMessage("プロセス検出"),
    "findProcessModeDesc": MessageLookupByLibrary.simpleMessage(
      "有効にすると、パフォーマンスが多少低下します",
    ),
    "followProfile": MessageLookupByLibrary.simpleMessage("プロファイルに従う"),
    "fontFamily": MessageLookupByLibrary.simpleMessage("フォント"),
    "forceRestartCoreTip": MessageLookupByLibrary.simpleMessage(
      "コアを強制再起動してもよろしいですか？",
    ),
    "fruitSaladScheme": MessageLookupByLibrary.simpleMessage("フルーツサラダ"),
    "general": MessageLookupByLibrary.simpleMessage("一般"),
    "geoAutoUpdate": MessageLookupByLibrary.simpleMessage("自動更新"),
    "geoAutoUpdateInterval": MessageLookupByLibrary.simpleMessage("自動更新間隔"),
    "geoAutoUpdateIntervalTip": MessageLookupByLibrary.simpleMessage(
      "自動更新間隔は0より大きくしてください",
    ),
    "geoOptions": MessageLookupByLibrary.simpleMessage("Geoオプション"),
    "geoResources": MessageLookupByLibrary.simpleMessage("Geoリソース"),
    "geoSkipped": m10,
    "geoUpdated": m11,
    "geodataLoader": MessageLookupByLibrary.simpleMessage("Geo低メモリモード"),
    "geodataLoaderDesc": MessageLookupByLibrary.simpleMessage(
      "有効にすると、低メモリのGeoローダーを使用します",
    ),
    "geoipCode": MessageLookupByLibrary.simpleMessage("GeoIPコード"),
    "global": MessageLookupByLibrary.simpleMessage("グローバル"),
    "go": MessageLookupByLibrary.simpleMessage("開く"),
    "goDownload": MessageLookupByLibrary.simpleMessage("ダウンロードへ"),
    "goToConfigureScript": MessageLookupByLibrary.simpleMessage("スクリプト設定へ移動"),
    "hasCacheChange": MessageLookupByLibrary.simpleMessage("変更をキャッシュしますか？"),
    "helperCorruptTip": MessageLookupByLibrary.simpleMessage(
      "Helper サービスが利用できないため、TUN モードを有効にできません。FlClash を再インストールしてください。",
    ),
    "hideFromList": MessageLookupByLibrary.simpleMessage("リストから隠す"),
    "hidePassword": MessageLookupByLibrary.simpleMessage("パスワードを隠す"),
    "host": MessageLookupByLibrary.simpleMessage("ホスト"),
    "hostsDesc": MessageLookupByLibrary.simpleMessage("Hostsを追加します"),
    "hotkeyConflict": MessageLookupByLibrary.simpleMessage("ホットキーが競合しています"),
    "hotkeyManagement": MessageLookupByLibrary.simpleMessage("ホットキー管理"),
    "hotkeyManagementDesc": MessageLookupByLibrary.simpleMessage(
      "キーボードでアプリを操作します",
    ),
    "hours": MessageLookupByLibrary.simpleMessage("時間"),
    "hoursAgo": m12,
    "hoursCount": m13,
    "icon": MessageLookupByLibrary.simpleMessage("アイコン"),
    "iconRecords": MessageLookupByLibrary.simpleMessage("アイコン履歴"),
    "iconStyle": MessageLookupByLibrary.simpleMessage("アイコンスタイル"),
    "iconUrl": MessageLookupByLibrary.simpleMessage("アイコンURL"),
    "ignoreBatteryOptimization": MessageLookupByLibrary.simpleMessage(
      "電池の最適化を無視",
    ),
    "import": MessageLookupByLibrary.simpleMessage("インポート"),
    "importFile": MessageLookupByLibrary.simpleMessage("ファイルからインポート"),
    "importFromURL": MessageLookupByLibrary.simpleMessage("URLからインポート"),
    "importUrl": MessageLookupByLibrary.simpleMessage("URLからインポート"),
    "inbound": MessageLookupByLibrary.simpleMessage("インバウンド"),
    "includeAllProxies": MessageLookupByLibrary.simpleMessage("すべてのプロキシを含める"),
    "includeAllProxiesTip": MessageLookupByLibrary.simpleMessage(
      "プロキシグループに属さないすべてのプロキシを取り込みます。下でプロキシグループを追加できます",
    ),
    "includeAllProxyProviders": MessageLookupByLibrary.simpleMessage(
      "すべてのプロキシプロバイダーを含める",
    ),
    "includeAllProxyProvidersTip": MessageLookupByLibrary.simpleMessage(
      "有効にすると、取り込んだプロキシプロバイダーを上書きします",
    ),
    "infiniteTime": MessageLookupByLibrary.simpleMessage("無期限"),
    "init": MessageLookupByLibrary.simpleMessage("初期化"),
    "inputCorrectHotkey": MessageLookupByLibrary.simpleMessage(
      "正しいホットキーを入力してください",
    ),
    "inputProxyGroupName": MessageLookupByLibrary.simpleMessage(
      "プロキシグループ名を入力してください",
    ),
    "inputRuleContent": MessageLookupByLibrary.simpleMessage("ルールの内容を入力してください"),
    "installedAppsPermissionDeniedMessage":
        MessageLookupByLibrary.simpleMessage(
          "アプリ一覧の権限が拒否されたため、インストール済みアプリを取得できません。システム設定から手動で許可してください。",
        ),
    "installedAppsPermissionDesc": MessageLookupByLibrary.simpleMessage(
      "このシステムでは、許可するまでインストール済みアプリの一覧が提供されません。許可すると、アプリごとのプロキシを設定できます。",
    ),
    "installedAppsPermissionRequired": MessageLookupByLibrary.simpleMessage(
      "アプリ一覧の権限が必要です",
    ),
    "intelligentSelected": MessageLookupByLibrary.simpleMessage("スマート選択"),
    "interfaceName": MessageLookupByLibrary.simpleMessage("インターフェース名"),
    "interfaceNameDesc": MessageLookupByLibrary.simpleMessage(
      "アウトバウンド接続に使用するネットワークインターフェース名",
    ),
    "interfaceNameMode": MessageLookupByLibrary.simpleMessage(
      "アウトバウンドインターフェース",
    ),
    "interfaceNameModeClear": MessageLookupByLibrary.simpleMessage("クリア"),
    "interfaceNameModeCustom": MessageLookupByLibrary.simpleMessage("カスタム"),
    "interfaceNameModeFollow": MessageLookupByLibrary.simpleMessage("設定に従う"),
    "internet": MessageLookupByLibrary.simpleMessage("インターネット"),
    "interval": MessageLookupByLibrary.simpleMessage("間隔"),
    "intranetIP": MessageLookupByLibrary.simpleMessage("イントラネットIP"),
    "invalidBackupFile": MessageLookupByLibrary.simpleMessage("無効なバックアップファイル"),
    "invalidPolicy": m14,
    "invalidProxy": m15,
    "invalidProxyProvider": m16,
    "invalidSubRule": m17,
    "ipcidr": MessageLookupByLibrary.simpleMessage("IP/CIDR"),
    "ipv6Desc": MessageLookupByLibrary.simpleMessage(
      "有効にすると、IPv6トラフィックを受信できます",
    ),
    "ipv6InboundDesc": MessageLookupByLibrary.simpleMessage("IPv6インバウンドを許可します"),
    "ja": MessageLookupByLibrary.simpleMessage("日本語"),
    "justNow": MessageLookupByLibrary.simpleMessage("たった今"),
    "keepAliveIntervalDesc": MessageLookupByLibrary.simpleMessage(
      "TCPキープアライブ間隔",
    ),
    "key": MessageLookupByLibrary.simpleMessage("キー"),
    "language": MessageLookupByLibrary.simpleMessage("言語"),
    "launchInterrupted": MessageLookupByLibrary.simpleMessage("起動が完了しませんでした"),
    "launchInterruptedTip": MessageLookupByLibrary.simpleMessage(
      "前回、アプリは起動中に予期せず終了しました。今回の自動セットアップはスキップしました。手動で起動して再試行できます。",
    ),
    "layout": MessageLookupByLibrary.simpleMessage("レイアウト"),
    "light": MessageLookupByLibrary.simpleMessage("ライト"),
    "list": MessageLookupByLibrary.simpleMessage("リスト"),
    "listen": MessageLookupByLibrary.simpleMessage("リッスン"),
    "loading": MessageLookupByLibrary.simpleMessage("読み込み中..."),
    "local": MessageLookupByLibrary.simpleMessage("ローカル"),
    "localBackupDesc": MessageLookupByLibrary.simpleMessage(
      "ローカルにデータをバックアップします",
    ),
    "locationPermission": MessageLookupByLibrary.simpleMessage("位置情報の権限"),
    "locationPermissionDeniedMessage": MessageLookupByLibrary.simpleMessage(
      "位置情報の権限が拒否されたため、現在の Wi-Fi 名を取得できません。システム設定で位置情報の権限を手動で有効にしてください。",
    ),
    "locationPermissionDesc": MessageLookupByLibrary.simpleMessage(
      "システムの要件により、Wi-Fi 名の取得には位置情報の権限が必要です。Android では「常に許可」を選択してください。そうしないと、アプリがバックグラウンドにあるときに Wi-Fi 名を取得できません。",
    ),
    "locationPermissionGuide": m18,
    "locationPermissionRequired": MessageLookupByLibrary.simpleMessage(
      "位置情報の権限が必要です",
    ),
    "log": MessageLookupByLibrary.simpleMessage("ログ"),
    "logLevel": MessageLookupByLibrary.simpleMessage("ログレベル"),
    "logcat": MessageLookupByLibrary.simpleMessage("ログキャプチャ"),
    "logcatDesc": MessageLookupByLibrary.simpleMessage("無効にするとログの入り口が非表示になります"),
    "logs": MessageLookupByLibrary.simpleMessage("ログ"),
    "logsDesc": MessageLookupByLibrary.simpleMessage("キャプチャしたログの記録"),
    "logsTest": MessageLookupByLibrary.simpleMessage("ログテスト"),
    "loopback": MessageLookupByLibrary.simpleMessage("ループバック解除ツール"),
    "loopbackDesc": MessageLookupByLibrary.simpleMessage("UWPのループバック解除に使用します"),
    "loose": MessageLookupByLibrary.simpleMessage("ゆったり"),
    "managedAccountTitle": MessageLookupByLibrary.simpleMessage("アカウントの状態"),
    "managedApplied": MessageLookupByLibrary.simpleMessage(
      "サーバー設定の準備が完了しました。接続時に計量の認可を再確認します。設定の更新だけでは接続しません。",
    ),
    "managedBalance": m19,
    "managedBusy": MessageLookupByLibrary.simpleMessage("アカウントの操作を処理中です。"),
    "managedCheck": MessageLookupByLibrary.simpleMessage("利用権限と設定を再確認"),
    "managedConfigUnavailable": MessageLookupByLibrary.simpleMessage(
      "設定が変更されたか利用できません。再確認してください。古い設定は使用しません。",
    ),
    "managedConnect": MessageLookupByLibrary.simpleMessage("接続"),
    "managedConnected": MessageLookupByLibrary.simpleMessage(
      "プロキシに接続中です。通信量を毎分報告します。",
    ),
    "managedConnecting": MessageLookupByLibrary.simpleMessage(
      "認可を確認しました。プラットフォームの接続を待っています。",
    ),
    "managedCoreStarting": MessageLookupByLibrary.simpleMessage(
      "安全なアカウント接続を準備しています…",
    ),
    "managedCoreUnavailable": MessageLookupByLibrary.simpleMessage(
      "Core に接続できません。再接続してからログインしてください。",
    ),
    "managedCounterError": MessageLookupByLibrary.simpleMessage(
      "コアの通信量カウンターに異常があります。コアを再起動してログインし直してください。",
    ),
    "managedCredentialStoreError": MessageLookupByLibrary.simpleMessage(
      "キーチェーンを利用できません。パスワードを手入力してください。平文では保存しません。削除に失敗した場合は、キーチェーンアクセスで本アプリの managed-login 項目を削除してください。",
    ),
    "managedCredentialsError": MessageLookupByLibrary.simpleMessage(
      "メールアドレスまたはパスワードが正しくありません。",
    ),
    "managedCumulativeTraffic": m20,
    "managedDeviceLimit": MessageLookupByLibrary.simpleMessage(
      "端末数の上限に達しました。他の端末を切断して再試行してください。",
    ),
    "managedDisconnect": MessageLookupByLibrary.simpleMessage("切断"),
    "managedEmail": MessageLookupByLibrary.simpleMessage("メールアドレス"),
    "managedEstimatedBalance": m21,
    "managedExitApp": MessageLookupByLibrary.simpleMessage("アプリを終了"),
    "managedExpires": m22,
    "managedFinalTrafficUnconfirmed": MessageLookupByLibrary.simpleMessage(
      "プロキシとローカルセッションを閉じましたが、終了期限内に最終通信量を確認できませんでした。精算は保証されず、表示残量を確定値として扱わないでください。",
    ),
    "managedIntro": MessageLookupByLibrary.simpleMessage(
      "アプリの起動ごとにログインが必要です。このアプリはパスワードやセッショントークンを保存しません。",
    ),
    "managedInvalidEmail": MessageLookupByLibrary.simpleMessage(
      "有効なメールアドレスを入力してください。",
    ),
    "managedInvalidPassword": MessageLookupByLibrary.simpleMessage(
      "72 UTF-8 バイト以内のパスワードを入力してください。",
    ),
    "managedLoading": MessageLookupByLibrary.simpleMessage("管理対象の設定を準備しています…"),
    "managedLogoutUnconfirmed": MessageLookupByLibrary.simpleMessage(
      "端末ではログアウトしましたが、サーバーの確認を取得できませんでした。このアプリは以前のセッションを再利用しません。",
    ),
    "managedNetworkError": MessageLookupByLibrary.simpleMessage(
      "アカウントサービスに接続できません。ネットワークを確認して再試行してください。",
    ),
    "managedNoConfiguration": MessageLookupByLibrary.simpleMessage(
      "有効なサーバー管理の設定がありません。",
    ),
    "managedPassword": MessageLookupByLibrary.simpleMessage("パスワード"),
    "managedPlan": m23,
    "managedProtocolError": MessageLookupByLibrary.simpleMessage(
      "サーバーの応答を検証できません。プロキシは停止したままです。",
    ),
    "managedQuotaExhausted": MessageLookupByLibrary.simpleMessage(
      "利用可能な通信量を使い切りました。",
    ),
    "managedReconnectRequired": MessageLookupByLibrary.simpleMessage(
      "認可または実行状態が変わったため停止しました。再接続して権限を確認してください。",
    ),
    "managedReload": MessageLookupByLibrary.simpleMessage("サーバー設定を再読み込み"),
    "managedRememberPassword": MessageLookupByLibrary.simpleMessage(
      "macOS キーチェーンにパスワードを保存",
    ),
    "managedRequestFailed": MessageLookupByLibrary.simpleMessage(
      "操作に失敗しました。再試行してください。",
    ),
    "managedRetryCore": MessageLookupByLibrary.simpleMessage("Core に再接続"),
    "managedSelectHint": MessageLookupByLibrary.simpleMessage(
      "現在のアカウントのサーバー設定に含まれるノードのみ選択できます。ノードを選択しても接続は開始されません。",
    ),
    "managedSessionExpired": MessageLookupByLibrary.simpleMessage(
      "セッションまたは利用権限が変更されました。再度ログインしてください。",
    ),
    "managedSignIn": MessageLookupByLibrary.simpleMessage("ログイン"),
    "managedSignInTitle": MessageLookupByLibrary.simpleMessage("アカウントにログイン"),
    "managedSignOut": MessageLookupByLibrary.simpleMessage("ログアウト / ログインを中止"),
    "managedStaged": MessageLookupByLibrary.simpleMessage(
      "設定をダウンロードし、整合性を検証しました。設定の適用と計量の有効化は次の統合段階で行うため、プロキシは停止したままです。",
    ),
    "managedStopped": MessageLookupByLibrary.simpleMessage(
      "プロキシは停止中です。既存のローカル設定でログインを回避できません。",
    ),
    "managedStorageError": MessageLookupByLibrary.simpleMessage(
      "管理対象の設定を安全に保存、適用、または削除できませんでした。プロキシは停止したままです。",
    ),
    "managedSubscriptionExpired": MessageLookupByLibrary.simpleMessage(
      "プランの有効期限が切れています。公式サイトで更新してください。",
    ),
    "managedTrafficPending": MessageLookupByLibrary.simpleMessage(
      "通信量の確認を待っています。再試行では同じバッチを送信します。",
    ),
    "managedTrafficUnconfirmed": MessageLookupByLibrary.simpleMessage(
      "プロキシは停止しましたが、最終通信量は未確認です。セッションを維持して再試行し、確認後にログアウトしてください。",
    ),
    "managedTunAuthorize": MessageLookupByLibrary.simpleMessage(
      "macOS TUN を認可してコアを再起動",
    ),
    "managedTunDescription": MessageLookupByLibrary.simpleMessage(
      "macOS ではシステムプロキシだけでなく、TUN で TCP・UDP・IPv6 を処理します。初回接続前に認可し、コアの再起動後に再ログインしてください。システム全体のキルスイッチではありません。",
    ),
    "managedTunFailed": MessageLookupByLibrary.simpleMessage(
      "TUN を安全に起動または終了できませんでした。システムプロキシのみへの切り替えは行いません。アプリを再起動し、他の VPN やネットワーク権限を確認してください。",
    ),
    "managedTunPermission": MessageLookupByLibrary.simpleMessage(
      "TUN には管理者権限が必要です。認可ボタンを押して再ログインしてください。プロキシは未接続です。",
    ),
    "managedTunRouteCheckFailed": MessageLookupByLibrary.simpleMessage(
      "システム経路を安全に確認できないため、プロキシを起動しませんでした。アプリを再起動し、ネットワーク権限を確認してから再試行してください。",
    ),
    "managedTunRouteConflict": MessageLookupByLibrary.simpleMessage(
      "別の VPN またはトンネルが必要なシステム経路を使用しています。Shadowrocket、Clash などの VPN アプリで接続を切断してから再接続してください。ウィンドウを閉じるだけでは切断されません。再認可は不要です。このプロキシは未接続で、他の VPN の経路を上書きしません。",
    ),
    "managedUnsupported": MessageLookupByLibrary.simpleMessage(
      "このサーバー設定には、管理対象クライアントが未対応の機能が含まれています。サービス管理者にお問い合わせください。",
    ),
    "managedUpgradeRequired": MessageLookupByLibrary.simpleMessage(
      "有料プランが必要です。公式サイトで更新後、利用権限を再確認してください。",
    ),
    "managedWebsite": MessageLookupByLibrary.simpleMessage("公式サイトとプラン"),
    "matchSourceIp": MessageLookupByLibrary.simpleMessage("送信元IPにマッチ"),
    "matchTarget": MessageLookupByLibrary.simpleMessage("MATCH-TARGET"),
    "matchTargetDesc": MessageLookupByLibrary.simpleMessage(
      "MATCH-TARGET を対象にしたルールの行き先。既定ではこのプロファイル末尾の MATCH ルールのターゲットを使います。",
    ),
    "matchTargetTitle": MessageLookupByLibrary.simpleMessage("マッチ先"),
    "maxFailedTimes": MessageLookupByLibrary.simpleMessage("最大失敗回数"),
    "maxLengthTip": m24,
    "maximize": MessageLookupByLibrary.simpleMessage("最大化"),
    "memoryInfo": MessageLookupByLibrary.simpleMessage("メモリ情報"),
    "messageTest": MessageLookupByLibrary.simpleMessage("メッセージテスト"),
    "messageTestTip": MessageLookupByLibrary.simpleMessage("これはメッセージです。"),
    "min": MessageLookupByLibrary.simpleMessage("最小"),
    "minimize": MessageLookupByLibrary.simpleMessage("最小化"),
    "minimizeOnExit": MessageLookupByLibrary.simpleMessage("終了時に最小化"),
    "minimizeOnExitDesc": MessageLookupByLibrary.simpleMessage(
      "システム標準の終了動作を変更します",
    ),
    "minutesAgo": m25,
    "mixedPort": MessageLookupByLibrary.simpleMessage("Mixedポート"),
    "mode": MessageLookupByLibrary.simpleMessage("モード"),
    "monochromeScheme": MessageLookupByLibrary.simpleMessage("モノクローム"),
    "monthsAgo": m26,
    "more": MessageLookupByLibrary.simpleMessage("その他"),
    "multipleValuesTip": MessageLookupByLibrary.simpleMessage(
      "複数の値はカンマで区切ってください",
    ),
    "name": MessageLookupByLibrary.simpleMessage("名前"),
    "nameserver": MessageLookupByLibrary.simpleMessage("ネームサーバー"),
    "nameserverDesc": MessageLookupByLibrary.simpleMessage("ドメインの名前解決に使用します"),
    "nameserverPolicy": MessageLookupByLibrary.simpleMessage("ネームサーバーポリシー"),
    "nameserverPolicyDesc": MessageLookupByLibrary.simpleMessage(
      "ドメインごとのネームサーバーポリシーを指定します",
    ),
    "network": MessageLookupByLibrary.simpleMessage("ネットワーク"),
    "networkDesc": MessageLookupByLibrary.simpleMessage("ネットワーク関連の設定を変更します"),
    "networkDetection": MessageLookupByLibrary.simpleMessage("ネットワーク検出"),
    "networkException": MessageLookupByLibrary.simpleMessage(
      "ネットワークエラーです。接続を確認してから再試行してください",
    ),
    "networkSpeed": MessageLookupByLibrary.simpleMessage("ネットワーク速度"),
    "networkType": MessageLookupByLibrary.simpleMessage("ネットワーク種別"),
    "neutralScheme": MessageLookupByLibrary.simpleMessage("ニュートラル"),
    "nextMatch": MessageLookupByLibrary.simpleMessage("次の一致"),
    "noData": MessageLookupByLibrary.simpleMessage("データがありません"),
    "noHotKey": MessageLookupByLibrary.simpleMessage("ホットキーはまだありません"),
    "noInfo": MessageLookupByLibrary.simpleMessage("情報がありません"),
    "noLongerRemind": MessageLookupByLibrary.simpleMessage("今後表示しない"),
    "noNetwork": MessageLookupByLibrary.simpleMessage("ネットワークがありません"),
    "noNetworkApp": MessageLookupByLibrary.simpleMessage("ネットワーク不使用アプリ"),
    "noRecords": MessageLookupByLibrary.simpleMessage("記録がありません"),
    "noResolve": MessageLookupByLibrary.simpleMessage("IPを解決しない"),
    "noResolveHostname": MessageLookupByLibrary.simpleMessage("ホスト名を解決しない"),
    "none": MessageLookupByLibrary.simpleMessage("なし"),
    "notSelectedTip": MessageLookupByLibrary.simpleMessage(
      "現在のプロキシグループは選択できません",
    ),
    "nullProfileDesc": MessageLookupByLibrary.simpleMessage(
      "プロファイルがありません。先にプロファイルを追加してください",
    ),
    "nullTip": m27,
    "numberTip": m28,
    "onDemand": MessageLookupByLibrary.simpleMessage("オンデマンド"),
    "onDemandDesc": MessageLookupByLibrary.simpleMessage(
      "特定のシナリオでのアプリの実行状態を設定します",
    ),
    "onlyIcon": MessageLookupByLibrary.simpleMessage("アイコンのみ"),
    "onlyStatisticsProxy": MessageLookupByLibrary.simpleMessage("プロキシのみ集計"),
    "onlyStatisticsProxyDesc": MessageLookupByLibrary.simpleMessage(
      "有効にすると、プロキシのトラフィックのみを集計します",
    ),
    "optional": MessageLookupByLibrary.simpleMessage("任意"),
    "options": MessageLookupByLibrary.simpleMessage("オプション"),
    "other": MessageLookupByLibrary.simpleMessage("その他"),
    "otherContributors": MessageLookupByLibrary.simpleMessage("その他の貢献者"),
    "outboundMode": MessageLookupByLibrary.simpleMessage("アウトバウンドモード"),
    "override": MessageLookupByLibrary.simpleMessage("上書き"),
    "overrideDns": MessageLookupByLibrary.simpleMessage("DNSを上書き"),
    "overrideDnsDesc": MessageLookupByLibrary.simpleMessage(
      "有効にすると、プロファイル内のDNS設定を上書きします",
    ),
    "overrideMode": MessageLookupByLibrary.simpleMessage("上書きモード"),
    "overrideScript": MessageLookupByLibrary.simpleMessage("上書きスクリプト"),
    "overwriteTypeCustom": MessageLookupByLibrary.simpleMessage("カスタム"),
    "overwriteTypeCustomDesc": MessageLookupByLibrary.simpleMessage(
      "カスタムモード：プロキシグループとルールを完全にカスタマイズできます",
    ),
    "palette": MessageLookupByLibrary.simpleMessage("パレット"),
    "password": MessageLookupByLibrary.simpleMessage("パスワード"),
    "paste": MessageLookupByLibrary.simpleMessage("貼り付け"),
    "pickFromAlbum": MessageLookupByLibrary.simpleMessage("アルバムから選択"),
    "pinWindow": MessageLookupByLibrary.simpleMessage("最前面に固定"),
    "pleaseBindWebDAV": MessageLookupByLibrary.simpleMessage("WebDAVを連携してください"),
    "pleaseEnterScriptName": MessageLookupByLibrary.simpleMessage(
      "スクリプト名を入力してください",
    ),
    "pleaseUploadValidQrcode": MessageLookupByLibrary.simpleMessage(
      "有効なQRコードをアップロードしてください",
    ),
    "port": MessageLookupByLibrary.simpleMessage("ポート"),
    "portConflictTip": MessageLookupByLibrary.simpleMessage("別のポートを入力してください"),
    "portTip": m29,
    "preferH3Desc": MessageLookupByLibrary.simpleMessage("DoHでHTTP/3を優先します"),
    "prerequisites": MessageLookupByLibrary.simpleMessage("前提条件"),
    "pressKeyboard": MessageLookupByLibrary.simpleMessage("キーを押してください"),
    "preview": MessageLookupByLibrary.simpleMessage("プレビュー"),
    "previousMatch": MessageLookupByLibrary.simpleMessage("前の一致"),
    "process": MessageLookupByLibrary.simpleMessage("プロセス"),
    "profile": MessageLookupByLibrary.simpleMessage("プロファイル"),
    "profileAutoUpdateIntervalInvalidValidationDesc":
        MessageLookupByLibrary.simpleMessage("有効な間隔を入力してください"),
    "profileAutoUpdateIntervalNullValidationDesc":
        MessageLookupByLibrary.simpleMessage("自動更新間隔を入力してください"),
    "profileHasUpdate": MessageLookupByLibrary.simpleMessage(
      "プロファイルが変更されています。自動更新を無効にしますか？",
    ),
    "profileNameNullValidationDesc": MessageLookupByLibrary.simpleMessage(
      "プロファイル名を入力してください",
    ),
    "profileUrlInvalidValidationDesc": MessageLookupByLibrary.simpleMessage(
      "有効なプロファイルURLを入力してください",
    ),
    "profileUrlNullValidationDesc": MessageLookupByLibrary.simpleMessage(
      "プロファイルのURLを入力してください",
    ),
    "profiles": MessageLookupByLibrary.simpleMessage("プロファイル"),
    "profilesSort": MessageLookupByLibrary.simpleMessage("プロファイルの並べ替え"),
    "project": MessageLookupByLibrary.simpleMessage("プロジェクト"),
    "providers": MessageLookupByLibrary.simpleMessage("外部リソース"),
    "proxies": MessageLookupByLibrary.simpleMessage("プロキシ"),
    "proxiesCount": m30,
    "proxiesEmpty": MessageLookupByLibrary.simpleMessage("プロキシが空です"),
    "proxyChains": MessageLookupByLibrary.simpleMessage("プロキシチェーン"),
    "proxyDetectedAbnormal": MessageLookupByLibrary.simpleMessage(
      "選択したプロキシに異常が見つかりました",
    ),
    "proxyFilter": MessageLookupByLibrary.simpleMessage("ノードフィルター"),
    "proxyGroup": MessageLookupByLibrary.simpleMessage("プロキシグループ"),
    "proxyGroupDetectedAbnormal": MessageLookupByLibrary.simpleMessage(
      "現在のプロキシグループに異常が見つかりました",
    ),
    "proxyGroupEmpty": MessageLookupByLibrary.simpleMessage("プロキシグループが空です"),
    "proxyGroupNameDuplicate": MessageLookupByLibrary.simpleMessage(
      "プロキシグループ名が重複しています",
    ),
    "proxyGroupNameEmpty": MessageLookupByLibrary.simpleMessage(
      "プロキシグループ名は空にできません",
    ),
    "proxyNameserver": MessageLookupByLibrary.simpleMessage("プロキシネームサーバー"),
    "proxyNameserverDesc": MessageLookupByLibrary.simpleMessage(
      "プロキシノードのドメイン解決に使用します",
    ),
    "proxyProviderDetectedAbnormal": MessageLookupByLibrary.simpleMessage(
      "選択したプロキシプロバイダーに異常が見つかりました",
    ),
    "proxyProviders": MessageLookupByLibrary.simpleMessage("プロキシプロバイダー"),
    "proxyProvidersEmpty": MessageLookupByLibrary.simpleMessage(
      "プロキシプロバイダーが空です",
    ),
    "proxyProvidersNotEmpty": MessageLookupByLibrary.simpleMessage(
      "プロキシプロバイダーは空にできません",
    ),
    "proxyType": MessageLookupByLibrary.simpleMessage("プロキシタイプ"),
    "pruneCache": MessageLookupByLibrary.simpleMessage("キャッシュを整理"),
    "pureBlackMode": MessageLookupByLibrary.simpleMessage("ピュアブラックモード"),
    "qrcode": MessageLookupByLibrary.simpleMessage("QRコード"),
    "qrcodeDesc": MessageLookupByLibrary.simpleMessage(
      "QRコードをスキャンしてプロファイルを取得します",
    ),
    "quickFill": MessageLookupByLibrary.simpleMessage("クイック入力"),
    "rainbowScheme": MessageLookupByLibrary.simpleMessage("レインボー"),
    "redirPort": MessageLookupByLibrary.simpleMessage("Redirポート"),
    "redo": MessageLookupByLibrary.simpleMessage("やり直す"),
    "remote": MessageLookupByLibrary.simpleMessage("リモート"),
    "remoteBackupDesc": MessageLookupByLibrary.simpleMessage(
      "WebDAVにデータをバックアップします",
    ),
    "remoteDestination": MessageLookupByLibrary.simpleMessage("リモート宛先"),
    "remove": MessageLookupByLibrary.simpleMessage("削除"),
    "request": MessageLookupByLibrary.simpleMessage("リクエスト"),
    "requests": MessageLookupByLibrary.simpleMessage("リクエスト"),
    "requestsDesc": MessageLookupByLibrary.simpleMessage("最近のリクエスト記録を表示します"),
    "reset": MessageLookupByLibrary.simpleMessage("リセット"),
    "resetPageChangesTip": MessageLookupByLibrary.simpleMessage(
      "このページには変更があります。リセットしてもよろしいですか？",
    ),
    "resetTip": MessageLookupByLibrary.simpleMessage("リセットしてもよろしいですか？"),
    "resources": MessageLookupByLibrary.simpleMessage("リソース"),
    "resourcesDesc": MessageLookupByLibrary.simpleMessage("外部リソースの関連情報"),
    "respectRules": MessageLookupByLibrary.simpleMessage("ルールに従う"),
    "respectRulesDesc": MessageLookupByLibrary.simpleMessage(
      "DNS接続がルールに従います。proxy-server-nameserverの設定が必要です",
    ),
    "restart": MessageLookupByLibrary.simpleMessage("再起動"),
    "restartCoreTip": MessageLookupByLibrary.simpleMessage("コアを再起動してもよろしいですか？"),
    "restore": MessageLookupByLibrary.simpleMessage("復元"),
    "restoreAllData": MessageLookupByLibrary.simpleMessage("すべてのデータを復元"),
    "restoreException": MessageLookupByLibrary.simpleMessage("復元エラー"),
    "restoreFromFileDesc": MessageLookupByLibrary.simpleMessage(
      "ファイルからデータを復元します",
    ),
    "restoreFromWebDAVDesc": MessageLookupByLibrary.simpleMessage(
      "WebDAVからデータを復元します",
    ),
    "restoreOnlyConfig": MessageLookupByLibrary.simpleMessage("プロファイルのみ復元"),
    "restoreStrategy": MessageLookupByLibrary.simpleMessage("復元方式"),
    "restoreStrategyCompatible": MessageLookupByLibrary.simpleMessage("互換"),
    "restoreStrategyOverride": MessageLookupByLibrary.simpleMessage("上書き"),
    "restoreSuccess": MessageLookupByLibrary.simpleMessage("復元が完了しました"),
    "routeAddress": MessageLookupByLibrary.simpleMessage("ルートアドレス"),
    "routeAddressDesc": MessageLookupByLibrary.simpleMessage(
      "リッスンするルートアドレスを設定します",
    ),
    "routeMode": MessageLookupByLibrary.simpleMessage("ルートモード"),
    "routeModeBypassPrivate": MessageLookupByLibrary.simpleMessage(
      "プライベートアドレスをバイパス",
    ),
    "routeModeConfig": MessageLookupByLibrary.simpleMessage("設定を使用"),
    "ru": MessageLookupByLibrary.simpleMessage("ロシア語"),
    "rule": MessageLookupByLibrary.simpleMessage("ルール"),
    "ruleActionAndDesc": MessageLookupByLibrary.simpleMessage("論理ルール AND"),
    "ruleActionDomainDesc": MessageLookupByLibrary.simpleMessage("完全なドメインにマッチ"),
    "ruleActionDomainKeywordDesc": MessageLookupByLibrary.simpleMessage(
      "ドメインキーワードにマッチ",
    ),
    "ruleActionDomainRegexDesc": MessageLookupByLibrary.simpleMessage(
      "ドメインの正規表現でマッチ",
    ),
    "ruleActionDomainSuffixDesc": MessageLookupByLibrary.simpleMessage(
      "ドメインサフィックスにマッチ",
    ),
    "ruleActionDomainWildcardDesc": MessageLookupByLibrary.simpleMessage(
      "ワイルドカードでマッチ（* と ? のみ対応）",
    ),
    "ruleActionDscpDesc": MessageLookupByLibrary.simpleMessage(
      "DSCPマークにマッチ（tproxy udpインバウンドのみ）",
    ),
    "ruleActionDstPortDesc": MessageLookupByLibrary.simpleMessage(
      "宛先ポート範囲にマッチ",
    ),
    "ruleActionGeoipDesc": MessageLookupByLibrary.simpleMessage("IPの国コードにマッチ"),
    "ruleActionGeositeDesc": MessageLookupByLibrary.simpleMessage(
      "Geosite 内のドメインにマッチ",
    ),
    "ruleActionInNameDesc": MessageLookupByLibrary.simpleMessage("インバウンド名にマッチ"),
    "ruleActionInPortDesc": MessageLookupByLibrary.simpleMessage(
      "インバウンドポートにマッチ",
    ),
    "ruleActionInTypeDesc": MessageLookupByLibrary.simpleMessage(
      "インバウンドタイプにマッチ",
    ),
    "ruleActionInUserDesc": MessageLookupByLibrary.simpleMessage(
      "インバウンドユーザー名にマッチ（/ で複数指定可）",
    ),
    "ruleActionIpAsnDesc": MessageLookupByLibrary.simpleMessage(
      "IPが属するASNにマッチ",
    ),
    "ruleActionIpCidr6Desc": MessageLookupByLibrary.simpleMessage(
      "IPアドレス範囲にマッチ（IP-CIDR6 は別名です）",
    ),
    "ruleActionIpCidrDesc": MessageLookupByLibrary.simpleMessage(
      "IPアドレス範囲にマッチ",
    ),
    "ruleActionIpSuffixDesc": MessageLookupByLibrary.simpleMessage(
      "IPサフィックス範囲にマッチ",
    ),
    "ruleActionMatchDesc": MessageLookupByLibrary.simpleMessage(
      "すべてのリクエストにマッチ（条件不要）",
    ),
    "ruleActionNetworkDesc": MessageLookupByLibrary.simpleMessage(
      "TCPまたはUDPにマッチ",
    ),
    "ruleActionNotDesc": MessageLookupByLibrary.simpleMessage("論理ルール NOT"),
    "ruleActionOrDesc": MessageLookupByLibrary.simpleMessage("論理ルール OR"),
    "ruleActionProcessNameDesc": MessageLookupByLibrary.simpleMessage(
      "プロセス名でマッチ（Androidではパッケージ名にマッチ）",
    ),
    "ruleActionProcessNameRegexDesc": MessageLookupByLibrary.simpleMessage(
      "プロセス名の正規表現でマッチ（Androidではパッケージ名にマッチ）",
    ),
    "ruleActionProcessNameWildcardDesc": MessageLookupByLibrary.simpleMessage(
      "プロセス名のワイルドカードでマッチ（* と ? のみ対応）",
    ),
    "ruleActionProcessPathDesc": MessageLookupByLibrary.simpleMessage(
      "プロセスのフルパスでマッチ",
    ),
    "ruleActionProcessPathRegexDesc": MessageLookupByLibrary.simpleMessage(
      "プロセスパスの正規表現でマッチ",
    ),
    "ruleActionProcessPathWildcardDesc": MessageLookupByLibrary.simpleMessage(
      "プロセスパスのワイルドカードでマッチ（* と ? のみ対応）",
    ),
    "ruleActionRematchNameDesc": MessageLookupByLibrary.simpleMessage(
      "再マッチ名にマッチ（複数は / で区切る）",
    ),
    "ruleActionRuleSetDesc": MessageLookupByLibrary.simpleMessage(
      "ルールセットを参照します。rule-providersの設定が必要です",
    ),
    "ruleActionSrcGeoipDesc": MessageLookupByLibrary.simpleMessage(
      "送信元IPの国コードにマッチ",
    ),
    "ruleActionSrcIpAsnDesc": MessageLookupByLibrary.simpleMessage(
      "送信元IPが属するASNにマッチ",
    ),
    "ruleActionSrcIpCidrDesc": MessageLookupByLibrary.simpleMessage(
      "送信元IPアドレス範囲にマッチ",
    ),
    "ruleActionSrcIpSuffixDesc": MessageLookupByLibrary.simpleMessage(
      "送信元IPサフィックス範囲にマッチ",
    ),
    "ruleActionSrcPortDesc": MessageLookupByLibrary.simpleMessage(
      "送信元ポート範囲にマッチ",
    ),
    "ruleActionSubRuleDesc": MessageLookupByLibrary.simpleMessage(
      "サブルールへマッチします。括弧の使い方に注意してください",
    ),
    "ruleActionUidDesc": MessageLookupByLibrary.simpleMessage(
      "LinuxのユーザーIDにマッチ",
    ),
    "ruleEmpty": MessageLookupByLibrary.simpleMessage("ルールが空です"),
    "ruleName": MessageLookupByLibrary.simpleMessage("ルール名"),
    "ruleSet": MessageLookupByLibrary.simpleMessage("ルールセット"),
    "ruleTarget": MessageLookupByLibrary.simpleMessage("ルールターゲット"),
    "rules": MessageLookupByLibrary.simpleMessage("ルール"),
    "rulesCount": m31,
    "save": MessageLookupByLibrary.simpleMessage("保存"),
    "saveChanges": MessageLookupByLibrary.simpleMessage("変更を保存しますか？"),
    "script": MessageLookupByLibrary.simpleMessage("スクリプト"),
    "scriptModeDesc": MessageLookupByLibrary.simpleMessage(
      "スクリプトモード：外部の拡張スクリプトを使用し、ワンクリックで設定を上書きします",
    ),
    "scrollToSelected": MessageLookupByLibrary.simpleMessage("選択項目へスクロール"),
    "search": MessageLookupByLibrary.simpleMessage("検索"),
    "seconds": MessageLookupByLibrary.simpleMessage("秒"),
    "secondsCount": m32,
    "selectAll": MessageLookupByLibrary.simpleMessage("すべて選択"),
    "selectMatchTarget": MessageLookupByLibrary.simpleMessage(
      "MATCH-TARGET を選択",
    ),
    "selectProxies": MessageLookupByLibrary.simpleMessage("プロキシを選択"),
    "selectProxyProviders": MessageLookupByLibrary.simpleMessage(
      "プロキシプロバイダーを選択",
    ),
    "selectRuleSet": MessageLookupByLibrary.simpleMessage("ルールセットを選択してください"),
    "selectSplitStrategy": MessageLookupByLibrary.simpleMessage(
      "振り分け戦略を選択してください",
    ),
    "selectSubRule": MessageLookupByLibrary.simpleMessage("サブルールを選択してください"),
    "selected": MessageLookupByLibrary.simpleMessage("選択済み"),
    "selectedCountTitle": m33,
    "settings": MessageLookupByLibrary.simpleMessage("設定"),
    "show": MessageLookupByLibrary.simpleMessage("表示"),
    "showLess": MessageLookupByLibrary.simpleMessage("折りたたむ"),
    "showMore": MessageLookupByLibrary.simpleMessage("展開"),
    "showNotificationStopAction": MessageLookupByLibrary.simpleMessage(
      "通知に停止ボタンを表示",
    ),
    "showNotificationStopActionDesc": MessageLookupByLibrary.simpleMessage(
      "常駐通知に停止ボタンを表示します。これが原因で通知が常に展開される場合はオフにしてください",
    ),
    "showPassword": MessageLookupByLibrary.simpleMessage("パスワードを表示"),
    "shrink": MessageLookupByLibrary.simpleMessage("コンパクト"),
    "silentLaunch": MessageLookupByLibrary.simpleMessage("サイレント起動"),
    "silentLaunchDesc": MessageLookupByLibrary.simpleMessage("バックグラウンドで起動します"),
    "size": MessageLookupByLibrary.simpleMessage("サイズ"),
    "socksPort": MessageLookupByLibrary.simpleMessage("SOCKSポート"),
    "sort": MessageLookupByLibrary.simpleMessage("並べ替え"),
    "source": MessageLookupByLibrary.simpleMessage("ソース"),
    "sourceIp": MessageLookupByLibrary.simpleMessage("送信元IP"),
    "specialProxy": MessageLookupByLibrary.simpleMessage("特殊プロキシ"),
    "specialRules": MessageLookupByLibrary.simpleMessage("特殊ルール"),
    "speedStatistics": MessageLookupByLibrary.simpleMessage("速度統計"),
    "splitStrategy": MessageLookupByLibrary.simpleMessage("振り分け戦略"),
    "splitStrategyNotEmpty": MessageLookupByLibrary.simpleMessage(
      "振り分け戦略は空にできません",
    ),
    "ssidsEmpty": MessageLookupByLibrary.simpleMessage("SSIDが空です"),
    "stackMode": MessageLookupByLibrary.simpleMessage("スタックモード"),
    "standard": MessageLookupByLibrary.simpleMessage("標準"),
    "standardModeDesc": MessageLookupByLibrary.simpleMessage(
      "標準モード：基本設定を上書きし、シンプルなルール追加機能を提供します",
    ),
    "start": MessageLookupByLibrary.simpleMessage("開始"),
    "startVpn": MessageLookupByLibrary.simpleMessage("VPNを起動しています..."),
    "status": MessageLookupByLibrary.simpleMessage("状態"),
    "statusDesc": MessageLookupByLibrary.simpleMessage("無効にすると、システムDNSを使用します"),
    "stop": MessageLookupByLibrary.simpleMessage("停止"),
    "stopVpn": MessageLookupByLibrary.simpleMessage("VPNを停止しています..."),
    "style": MessageLookupByLibrary.simpleMessage("スタイル"),
    "subRule": MessageLookupByLibrary.simpleMessage("サブルール"),
    "subRuleEmpty": MessageLookupByLibrary.simpleMessage("サブルールが空です"),
    "subRuleNotEmpty": MessageLookupByLibrary.simpleMessage("サブルールは空にできません"),
    "submit": MessageLookupByLibrary.simpleMessage("送信"),
    "subscriptionInfo": MessageLookupByLibrary.simpleMessage("サブスクリプション情報"),
    "suspended": MessageLookupByLibrary.simpleMessage("一時停止中..."),
    "sync": MessageLookupByLibrary.simpleMessage("同期"),
    "system": MessageLookupByLibrary.simpleMessage("システム"),
    "systemApp": MessageLookupByLibrary.simpleMessage("システムアプリ"),
    "systemProxy": MessageLookupByLibrary.simpleMessage("システムプロキシ"),
    "systemProxyDesc": MessageLookupByLibrary.simpleMessage("システムプロキシを設定します"),
    "tab": MessageLookupByLibrary.simpleMessage("タブ"),
    "tabAnimation": MessageLookupByLibrary.simpleMessage("タブアニメーション"),
    "tabAnimationDesc": MessageLookupByLibrary.simpleMessage("モバイル表示でのみ有効です"),
    "tapToAuthorize": MessageLookupByLibrary.simpleMessage("タップして許可"),
    "tcpConcurrent": MessageLookupByLibrary.simpleMessage("TCP同時接続"),
    "tcpConcurrentDesc": MessageLookupByLibrary.simpleMessage(
      "有効にすると、TCPの同時接続を許可します",
    ),
    "testInterval": MessageLookupByLibrary.simpleMessage("テスト間隔"),
    "testUrl": MessageLookupByLibrary.simpleMessage("テストURL"),
    "testWhenUsed": MessageLookupByLibrary.simpleMessage("使用時にテスト"),
    "textScale": MessageLookupByLibrary.simpleMessage("テキストの拡大縮小"),
    "theme": MessageLookupByLibrary.simpleMessage("テーマ"),
    "themeColor": MessageLookupByLibrary.simpleMessage("テーマカラー"),
    "themeDesc": MessageLookupByLibrary.simpleMessage("ダークモードの設定と色の調整"),
    "themeMode": MessageLookupByLibrary.simpleMessage("テーマモード"),
    "tight": MessageLookupByLibrary.simpleMessage("コンパクト"),
    "time": MessageLookupByLibrary.simpleMessage("時刻"),
    "timeout": MessageLookupByLibrary.simpleMessage("タイムアウト"),
    "tip": MessageLookupByLibrary.simpleMessage("ヒント"),
    "toggle": MessageLookupByLibrary.simpleMessage("切り替え"),
    "toggleLabel": MessageLookupByLibrary.simpleMessage("ラベルを切り替え"),
    "tonalSpotScheme": MessageLookupByLibrary.simpleMessage("トーナルスポット"),
    "tools": MessageLookupByLibrary.simpleMessage("ツール"),
    "torch": MessageLookupByLibrary.simpleMessage("ライト"),
    "totalTraffic": MessageLookupByLibrary.simpleMessage("合計トラフィック"),
    "tproxyPort": MessageLookupByLibrary.simpleMessage("TProxyポート"),
    "trafficUsage": MessageLookupByLibrary.simpleMessage("トラフィック統計"),
    "tun": MessageLookupByLibrary.simpleMessage("TUN"),
    "tunDesc": MessageLookupByLibrary.simpleMessage("管理者モードでのみ有効"),
    "turnOff": MessageLookupByLibrary.simpleMessage("オフにする"),
    "turnOn": MessageLookupByLibrary.simpleMessage("オンにする"),
    "undo": MessageLookupByLibrary.simpleMessage("元に戻す"),
    "unifiedDelay": MessageLookupByLibrary.simpleMessage("統一遅延"),
    "unifiedDelayDesc": MessageLookupByLibrary.simpleMessage(
      "ハンドシェイクなどの余分な遅延を除きます",
    ),
    "unknown": MessageLookupByLibrary.simpleMessage("不明"),
    "unknownNetworkError": MessageLookupByLibrary.simpleMessage("不明なネットワークエラー"),
    "unmaximize": MessageLookupByLibrary.simpleMessage("元に戻す"),
    "unnamed": MessageLookupByLibrary.simpleMessage("名称未設定"),
    "unpinWindow": MessageLookupByLibrary.simpleMessage("固定を解除"),
    "update": MessageLookupByLibrary.simpleMessage("更新"),
    "upload": MessageLookupByLibrary.simpleMessage("アップロード"),
    "url": MessageLookupByLibrary.simpleMessage("URL"),
    "urlDesc": MessageLookupByLibrary.simpleMessage("URLからプロファイルを取得します"),
    "urlTip": m34,
    "useHosts": MessageLookupByLibrary.simpleMessage("Hostsを使用"),
    "useSystemHosts": MessageLookupByLibrary.simpleMessage("システムのHostsを使用"),
    "usedTraffic": MessageLookupByLibrary.simpleMessage("使用済みトラフィック"),
    "userAgent": MessageLookupByLibrary.simpleMessage("User-Agent"),
    "value": MessageLookupByLibrary.simpleMessage("値"),
    "vibrantScheme": MessageLookupByLibrary.simpleMessage("ビブラント"),
    "view": MessageLookupByLibrary.simpleMessage("表示"),
    "vpnConfigChangeDetected": MessageLookupByLibrary.simpleMessage(
      "VPN関連の設定変更を検出しました",
    ),
    "vpnEnableDesc": MessageLookupByLibrary.simpleMessage(
      "VpnServiceでシステムの全トラフィックを自動的にルーティングします",
    ),
    "vpnTip": MessageLookupByLibrary.simpleMessage("変更はVPNの再起動後に有効になります"),
    "webDAVConfiguration": MessageLookupByLibrary.simpleMessage("WebDAV設定"),
    "whitelistMode": MessageLookupByLibrary.simpleMessage("ホワイトリストモード"),
    "yearsAgo": m35,
    "zhCN": MessageLookupByLibrary.simpleMessage("簡体字中国語"),
  };
}

// The vocabulary of the runtime copy guards (ui-copy.spec, home.spec). The source scan
// scripts/copy-guard.test.mjs keeps the same lists and explains each word.
export const developmentWords = [
  '开发',
  '联调',
  '测试',
  '模拟',
  '演示',
  '尚未接入',
  '待接入',
  '后端',
  '前端',
  '数据库',
  '快照',
  '服务端',
  '计量',
  '心跳',
  '联系管理员',
  '管理员配置',
];
// Claims the service cannot prove and the advertising law forbids (handoff_ui.md D12, D5).
export const claimWords = [
  '最快',
  '最好',
  '最强',
  '最稳定',
  '最安全',
  '最低价',
  '第一品牌',
  '行业第一',
  '全网第一',
  '国内第一',
  'No.1',
  '100% 安全',
  '100%安全',
  '百分百',
  '百分之百',
  '零日志',
  '无日志',
  '不限速',
  '无限速',
  '军用级',
  '军工级',
  '绝对安全',
  '永久免费',
  '翻墙',
  '科学上网',
];
export const bannedWords = [...developmentWords, ...claimWords];
// Wording that is not ours: Apple's own dialog and settings label for an enterprise-signed app.
export const allowedPhrases = ['未受信任的开发者', '信任该开发者'];

export function bannedIn(text: string): string[] {
  let rest = text;
  for (const phrase of allowedPhrases) rest = rest.replaceAll(phrase, '');
  return bannedWords.filter((word) => rest.includes(word));
}

// The content of the "使用教程" page (handoff_guide.md 4 and 5): plain data, no React, so node --test
// can check it. The button names in quotes are the clients' own words (the check table is in
// handoff_guide.md 15). Desktop texts never use the words the clients hide (VPN, engine, protocol …,
// handoff_guide.md D10); the mobile texts may, because the system dialogs say VPN themselves.

export type PlatformId = 'windows' | 'macos' | 'android' | 'ios';
export const platformIds: PlatformId[] = ['windows', 'macos', 'android', 'ios'];

/** Site paths a guide text may link to. */
export const guideLinkPaths = [
  '/register',
  '/plans',
  '/download',
  '/dashboard',
  '/tickets',
  '/forgot-password',
  '/guide',
];

export type Highlight = {
  /** The number drawn in the circle; the step text names the same number. */
  n: number;
  /** Percent of the picture: left, top, width, height (0–100). */
  rect: [number, number, number, number];
  shape?: 'circle' | 'box';
};

export type Step = {
  title: string;
  /** Plain text. "**x**" is emphasis, "[x](/path)" a link to a site path in guideLinkPaths. */
  body: string;
  /** Key into the picture manifest (round 2); steps without a picture are text only. */
  shot?: string;
  highlights?: Highlight[];
  /** The step that fetches the installer: the page shows the download button or the support hint. */
  download?: boolean;
};

export type Section = {
  id: string;
  title: string;
  steps: Step[];
  note?: { kind: 'tip' | 'warn'; text: string };
};

export type Faq = { q: string; a: string };

export type Platform = {
  id: PlatformId;
  name: string;
  icon: 'windows' | 'apple' | 'android' | 'phone';
  /** One line: the system and chip the client needs. */
  device: string;
  /** Which entry of the download page answers for this platform (null: no public package yet, D6). */
  downloadKey: 'android' | 'ios' | null;
  sections: Section[];
  faq: Faq[];
};

/** The sections every platform has, in order (handoff_guide.md 4.1). */
export const requiredSections = [
  'device',
  'download',
  'authorize',
  'login',
  'lines',
  'connect',
  'usage',
  'background',
  'upgrade',
] as const;

export const beforeYouStart: { title: string; body: string }[] = [
  { title: '注册账号', body: '先在网站[注册](/register)一个账号，记住邮箱和密码。' },
  {
    title: '购买套餐',
    body: '到[套餐与订阅](/plans)选一个套餐。没有有效套餐时，客户端能登录但不能连接。',
  },
  {
    title: '了解设备数量',
    body: '同一账号可同时在线的设备数以套餐为准，在[总览](/dashboard)页可以看到正在使用的设备。',
  },
  { title: '用同一个账号登录', body: '客户端里填写的邮箱和密码，就是你在网站注册时用的那一组。' },
];

const lineExplain =
  '“专线”和“普通线路”是两类线路，购买的套餐决定能用哪些。每条线路都标有“线路倍率”：倍率 1.5× 表示实际用掉 1 GB，会按 1.5 GB 扣减套餐流量；倍率 1× 就是用多少扣多少。';

const windows: Platform = {
  id: 'windows',
  name: 'Windows',
  icon: 'windows',
  device:
    'Windows 10（2004，版本号 19041）及以上，或 Windows 11；仅支持 64 位 Intel / AMD 电脑（x64）',
  downloadKey: null,
  sections: [
    {
      id: 'device',
      title: '适用设备',
      steps: [
        {
          title: '确认电脑满足要求',
          body: '需要 Windows 10 2004 及以上版本，或 Windows 11，并且是 64 位 Intel / AMD 电脑。暂不支持 ARM 版 Windows。查看方法：打开“设置”，进入“系统”，再点“系统信息”。',
        },
      ],
    },
    {
      id: 'download',
      title: '第一步 下载安装',
      steps: [
        {
          title: '获取安装程序',
          body: '安装程序的文件名形如 AsterLink-Setup-x64-版本号.exe。下载页列出安装包时可直接下载；没有列出时，请联系客服获取。',
          download: true,
        },
        {
          title: '运行安装程序',
          body: '双击安装程序。如果出现“Windows 已保护你的电脑”，点“更多信息”，再点“仍要运行”。',
        },
        {
          title: '允许管理员权限',
          body: '出现“用户账户控制”提示时点“是”。如果当前不是管理员账户，Windows 会要求输入管理员的密码。',
        },
        {
          title: '完成安装并启动',
          body: '安装结束页勾选“启动 AsterLink”，点“完成”，AsterLink 就会打开。',
        },
      ],
    },
    {
      id: 'authorize',
      title: '第二步 首次授权',
      steps: [
        {
          title: '看登录页上方有没有“后台服务”卡片',
          body: '第一次使用时，登录页上方可能出现“后台服务”卡片。没有出现就直接做下一步。',
        },
        {
          title: '点卡片上的按钮',
          body: '卡片上的按钮会显示为“安装（需要管理员权限）”“修复（需要管理员权限）”或“启动（需要管理员权限）”，点它并在“用户账户控制”中点“是”。完成后会自动继续，必要时点“重新检测”。',
        },
      ],
      note: { kind: 'tip', text: '这一步每台电脑通常只需要做一次。' },
    },
    {
      id: 'login',
      title: '第三步 登录',
      steps: [
        {
          title: '输入邮箱和密码',
          body: '填写网站注册时的邮箱和密码，点“登录”。点密码框右侧的“显示密码”按钮可以查看输入的内容。',
        },
        {
          title: '选择是否记住密码',
          body: '勾选“记住密码”，下次打开会自动登录；密码只保存在这台电脑的凭据管理器里。忘记密码点“忘记密码”，会打开网站的[找回密码](/forgot-password)页面。',
        },
      ],
      note: { kind: 'warn', text: '点“退出登录”会清除这台电脑上保存的密码。' },
    },
    {
      id: 'lines',
      title: '第四步 选择线路',
      steps: [
        {
          title: '打开“线路”',
          body: '点窗口左侧的“线路”。可以在上方搜索，也可以按“全部”“专线”“普通线路”筛选，线路按地区分组。',
        },
        { title: '点选一条线路', body: '点一条线路，它会出现“已选”标记。' },
        { title: '看懂线路倍率', body: lineExplain },
      ],
      note: { kind: 'tip', text: '已连接时换线路，会先提示“将断开当前连接”，确认后需要重新连接。' },
    },
    {
      id: 'connect',
      title: '第五步 连接与断开',
      steps: [
        { title: '回到“连接”', body: '点窗口左侧的“连接”，页面中间是一个大圆形按钮。' },
        {
          title: '点圆形按钮连接',
          body: '状态会依次显示“正在准备”“正在连接”，成功后显示“已连接”和连接时长。页面下方会显示“本次已用 X · 按倍率计 Y”。',
        },
        { title: '再点一次断开', body: '连接中再点同一个按钮即可断开，状态变回“未连接”。' },
      ],
    },
    {
      id: 'usage',
      title: '第六步 查看套餐与用量',
      steps: [
        {
          title: '在“连接”页看套餐卡',
          body: '套餐卡上有剩余流量和到期时间。看到“正在同步用量”表示刚用的流量正在上报，稍等片刻就会更新。',
        },
        {
          title: '续费与升级',
          body: '套餐用完或到期后，到网站的[套餐与订阅](/plans)续费，回到 AsterLink 会自动同步。',
        },
        { title: '看账户信息', body: '点“账户”可以查看账户、套餐、外观设置，以及版本信息。' },
      ],
    },
    {
      id: 'background',
      title: '后台运行与退出',
      steps: [
        {
          title: '关闭窗口不会退出',
          body: '点窗口右上角的“×”，AsterLink 会缩到屏幕右下角的任务栏通知区域继续运行，连接不受影响。第一次关闭时会有一条提示。',
        },
        {
          title: '找回图标',
          body: '图标可能被收在“^”（显示隐藏的图标）里。点它可以打开窗口；右键图标，菜单里有“打开 AsterLink”“连接”（连接后变为“断开”）和“退出”。',
        },
        {
          title: '彻底退出',
          body: '右键图标选“退出”，或在“账户”页点“退出 AsterLink”。退出前会先断开连接。',
        },
      ],
    },
    {
      id: 'upgrade',
      title: '升级与卸载',
      steps: [
        {
          title: '升级',
          body: '先从任务栏通知区域的图标选“退出”，再运行新版本的安装程序覆盖安装。提示需要先退出时，退出后重试即可。',
        },
        {
          title: '卸载',
          body: '打开“设置”，进入“应用”，找到 AsterLink 点“卸载”。卸载时如果询问是否删除保存的密码和偏好，默认选“否”。',
        },
      ],
      note: {
        kind: 'tip',
        text: '选择删除后重新安装，会被当作一台新设备，可能暂时多占一个在线设备名额。',
      },
    },
  ],
  faq: [
    {
      q: '找不到右下角的 AsterLink 图标怎么办？',
      a: '点任务栏右下角的“^”展开隐藏的图标。还是没有，说明程序已经退出，从开始菜单重新打开 AsterLink。',
    },
    {
      q: '提示后台服务异常怎么办？',
      a: '先退出登录回到登录页，按“后台服务”卡片上的按钮完成安装、修复或启动，并同意管理员权限；也可以在“账户”页的“后台服务”部分点“重新检测”。',
    },
    {
      q: '升级时提示要先退出怎么办？',
      a: '右键任务栏通知区域的 AsterLink 图标，选“退出”，然后重新运行安装程序。',
    },
    { q: '可以装在 ARM 版 Windows 上吗？', a: '暂时不可以，目前只支持 64 位 Intel / AMD 电脑。' },
  ],
};

const macos: Platform = {
  id: 'macos',
  name: 'macOS',
  icon: 'apple',
  device: 'macOS 15 及以上，Apple 芯片的 Mac（M 系列）',
  downloadKey: null,
  sections: [
    {
      id: 'device',
      title: '适用设备',
      steps: [
        {
          title: '确认 Mac 满足要求',
          body: '需要 macOS 15 及以上，并且是 Apple 芯片（M1 及更新）的 Mac。暂不支持 Intel 芯片的 Mac。查看方法：点屏幕左上角的苹果菜单，选“关于本机”。',
        },
      ],
    },
    {
      id: 'download',
      title: '第一步 下载安装',
      steps: [
        {
          title: '获取安装包',
          body: '安装包是一个 DMG 文件。下载页列出安装包时可直接下载；没有列出时，请联系客服获取。',
          download: true,
        },
        {
          title: '拖进“应用程序”',
          body: '双击打开 DMG，把 AsterLink 图标拖到旁边的“应用程序”文件夹。',
        },
        {
          title: '从“应用程序”里打开',
          body: '一定要从“应用程序”文件夹里打开 AsterLink，直接在 DMG 窗口里运行会提示先拖过去。',
        },
        {
          title: '遇到“无法验证”提示',
          body: '如果系统提示无法验证、阻止打开，打开“系统设置”，进入“隐私与安全性”，在页面下方点“仍要打开”，再确认一次。',
        },
      ],
    },
    {
      id: 'authorize',
      title: '第二步 首次授权',
      steps: [
        {
          title: '点“启用”',
          body: '登录页会显示“首次使用：允许 AsterLink 在后台运行”卡片，点“启用”。',
        },
        {
          title: '在系统设置里打开开关',
          body: '系统设置会自动打开“通用 › 登录项与扩展”。在“允许在后台”里，打开 AsterLink 的开关，可能需要输入这台 Mac 的密码。',
        },
        {
          title: '回到 AsterLink',
          body: '回到 AsterLink，它会自动继续，不需要点刷新。如果开关没找到，点卡片上的“再次打开系统设置”。',
        },
        {
          title: '遇到钥匙串提示',
          body: '如果出现“AsterLink 想要使用钥匙串中的……”，点“始终允许”。',
        },
      ],
      note: { kind: 'tip', text: '这是 macOS 对所有常驻后台程序的统一要求，只需允许一次。' },
    },
    {
      id: 'login',
      title: '第三步 登录',
      steps: [
        { title: '输入邮箱和密码', body: '填写网站注册时的邮箱和密码，点“登录”。' },
        {
          title: '选择是否记住密码',
          body: '勾选“记住密码”，下次打开会自动登录；密码只保存在这台 Mac 的钥匙串里。忘记密码点“忘记密码”，会打开网站的[找回密码](/forgot-password)页面。',
        },
      ],
      note: { kind: 'warn', text: '点“退出登录”会清除这台 Mac 上保存的密码。' },
    },
    {
      id: 'lines',
      title: '第四步 选择线路',
      steps: [
        {
          title: '打开“线路”',
          body: '点窗口左侧的“线路”。可以搜索，也可以按“全部”“专线”“普通线路”筛选，线路按地区分组。',
        },
        { title: '点选一条线路', body: '点一条线路，它会出现“已选”标记。' },
        { title: '看懂线路倍率', body: lineExplain },
      ],
      note: { kind: 'tip', text: '已连接时换线路，会先提示“将断开当前连接”，确认后需要重新连接。' },
    },
    {
      id: 'connect',
      title: '第五步 连接与断开',
      steps: [
        { title: '回到“连接”', body: '点窗口左侧的“连接”，页面中间是一个大圆形按钮。' },
        {
          title: '点圆形按钮连接',
          body: '状态会依次显示“正在准备”“正在连接”，成功后显示“已连接”和连接时长。页面下方会显示“本次已用 X · 按倍率计 Y”。',
        },
        { title: '再点一次断开', body: '连接中再点同一个按钮即可断开，状态变回“未连接”。' },
      ],
    },
    {
      id: 'usage',
      title: '第六步 查看套餐与用量',
      steps: [
        {
          title: '在“连接”页看套餐卡',
          body: '套餐卡上有剩余流量和到期时间。看到“正在同步用量”表示刚用的流量正在上报，稍等片刻就会更新。',
        },
        {
          title: '续费与升级',
          body: '套餐用完或到期后，到网站的[套餐与订阅](/plans)续费，回到 AsterLink 会自动同步。',
        },
        { title: '看账户信息', body: '点“账户”可以查看账户、套餐、外观设置，以及版本信息。' },
      ],
    },
    {
      id: 'background',
      title: '后台运行与退出',
      steps: [
        {
          title: '关闭窗口不会退出',
          body: '关闭主窗口后，Dock 里的图标会消失，只在屏幕顶部的菜单栏留下 AsterLink 图标，连接不受影响。',
        },
        {
          title: '找回窗口',
          body: '点菜单栏里的 AsterLink 图标，选“打开 AsterLink”。菜单栏太满时图标可能被刘海遮住，可以先退出几个别的菜单栏程序。',
        },
        {
          title: '彻底退出',
          body: '点菜单栏图标选“退出”，或按 ⌘Q，或在“账户”页点“退出 AsterLink”。',
        },
      ],
    },
    {
      id: 'upgrade',
      title: '升级与卸载',
      steps: [
        {
          title: '升级',
          body: '先断开连接，用新版本替换“应用程序”里的 AsterLink，再打开。如果提示“后台服务需要更新”，到“账户”页按提示点“重新安装后台服务”。',
        },
        {
          title: '卸载',
          body: '卸载前，先到“账户”页的“后台服务”部分点“移除后台服务”，再使用安装包里附带的卸载程序。',
        },
      ],
    },
  ],
  faq: [
    {
      q: '为什么提示“后台服务尚未启用”？',
      a: '“允许在后台”里 AsterLink 的开关被关掉了。到“系统设置 › 通用 › 登录项与扩展”重新打开，或在“账户”页的“后台服务”部分点“启用”。',
    },
    {
      q: '找不到菜单栏图标怎么办？',
      a: '菜单栏图标太多时会被刘海遮住。退出几个不常用的菜单栏程序，或从“应用程序”里再次打开 AsterLink 即可回到窗口。',
    },
    { q: '可以装在 Intel 芯片的 Mac 上吗？', a: '暂时不可以，目前只支持 Apple 芯片的 Mac。' },
  ],
};

const android: Platform = {
  id: 'android',
  name: 'Android',
  icon: 'android',
  device: 'Android 8.0 及以上，64 位 ARM 手机（arm64-v8a）',
  downloadKey: 'android',
  sections: [
    {
      id: 'device',
      title: '适用设备',
      steps: [
        {
          title: '确认手机满足要求',
          body: '需要 Android 8.0 及以上的 64 位手机。查看方法：打开“设置”，进入“关于手机”。',
        },
      ],
    },
    {
      id: 'download',
      title: '第一步 下载安装',
      steps: [
        {
          title: '用手机浏览器下载',
          body: '在手机浏览器里打开[下载页](/download)，点“下载 APK”。',
          download: true,
        },
        {
          title: '允许安装',
          body: '下载完成后点“打开”。如果系统提示不允许安装未知应用，点“设置”，允许当前使用的浏览器安装应用，然后返回继续安装。',
        },
        { title: '完成安装', body: '点“安装”，安装结束后点“打开”。' },
      ],
      note: {
        kind: 'tip',
        text: '想核对文件是否完整，可以把下载页给出的 SHA-256 与下载文件对照；不核对也不影响使用。',
      },
    },
    {
      id: 'authorize',
      title: '第二步 首次授权',
      steps: [
        {
          title: '允许 VPN 连接',
          body: '第一次点连接时，系统会弹出 VPN 连接请求，点“确定”（或“允许”）。',
        },
        {
          title: '允许通知',
          body: 'Android 13 及以上会询问是否允许通知，建议允许：通知栏会显示连接状态、实时速率和断开原因。',
        },
      ],
      note: { kind: 'tip', text: '不需要 root。如果点了拒绝，再点一次连接按钮就会重新询问。' },
    },
    {
      id: 'login',
      title: '第三步 登录',
      steps: [
        { title: '输入邮箱和密码', body: '填写网站注册时的邮箱和密码，点“登录”。' },
        {
          title: '选择是否记住密码',
          body: '勾选“记住密码”，下次打开会自动登录；密码经系统密钥库加密，只保存在本机。还没有账号点“注册账号”，忘记密码点“忘记密码”。',
        },
      ],
      note: { kind: 'warn', text: '“我的”页的“退出登录”会断开连接，并删除本机保存的密码。' },
    },
    {
      id: 'lines',
      title: '第四步 选择线路',
      steps: [
        {
          title: '打开“线路”',
          body: '点底部的“线路”标签。可以搜索线路或地区，也可以按“全部”“专线”“普通”筛选。',
        },
        { title: '点选一条线路', body: '点一条线路，它会出现“已选择”标记。' },
        { title: '看懂线路倍率', body: lineExplain },
      ],
      note: {
        kind: 'tip',
        text: '已连接时换线路，会先弹出“切换线路？”，点“断开并切换”后需要重新连接。',
      },
    },
    {
      id: 'connect',
      title: '第五步 连接与断开',
      steps: [
        { title: '回到“连接”', body: '点底部的“连接”标签，页面中间是一个大圆形按钮。' },
        {
          title: '点圆形按钮连接',
          body: '状态会依次显示“正在准备”“正在连接”，成功后显示“已连接”和连接时长，下方显示“本次已用 X · 按倍率计 Y”。',
        },
        {
          title: '再点一次断开',
          body: '也可以在通知栏的 AsterLink 通知上点“断开”。断开后状态变回“未连接”。',
        },
      ],
    },
    {
      id: 'usage',
      title: '第六步 查看套餐与用量',
      steps: [
        {
          title: '在“连接”页看套餐卡',
          body: '套餐卡上有剩余流量和到期时间。看到“正在同步用量”表示刚用的流量正在上报，稍等片刻就会更新。',
        },
        {
          title: '续费与升级',
          body: '点“我的”页的“续费或升级”，或到网站的[套餐与订阅](/plans)，回到 AsterLink 会自动同步。',
        },
      ],
    },
    {
      id: 'background',
      title: '后台运行与省电',
      steps: [
        {
          title: '通知栏里的状态',
          body: '连接期间通知栏会一直显示 AsterLink 的通知。你也可以把 AsterLink 的快捷开关添加到下拉菜单里，一键连接或断开。',
        },
        {
          title: '设置后台运行',
          body: '点“我的”页的“后台运行与省电”，按提示把 AsterLink 设为“不限制”。小米、华为、OPPO、vivo 等品牌的入口名称不同，一般在“电池”或“应用管理”里。',
        },
        {
          title: '关于“始终开启的 VPN”',
          body: '本版本不支持系统的“始终开启的 VPN”，请在 AsterLink 里连接和断开。',
        },
      ],
    },
    {
      id: 'upgrade',
      title: '升级与卸载',
      steps: [
        {
          title: '升级',
          body: '回到[下载页](/download)下载新版本直接覆盖安装，记住的密码与设置会保留。系统不允许安装比现有版本更旧的版本。',
        },
        {
          title: '卸载',
          body: '在系统设置的应用列表里卸载 AsterLink，VPN 配置、记住的密码和设备标识会一并删除。',
        },
      ],
    },
  ],
  faq: [
    {
      q: '拒绝了 VPN 连接请求怎么办？',
      a: '界面会提示需要允许 AsterLink 创建 VPN 连接。再点一次圆形连接按钮，在弹出的系统请求里点“确定”即可。',
    },
    {
      q: '连接被其他 VPN 应用顶掉了怎么办？',
      a: '同一时间手机只能运行一个 VPN。关闭其他 VPN 应用，回到 AsterLink 重新连接。',
    },
    { q: '提示“应用版本过低”怎么办？', a: '回到[下载页](/download)下载最新版本覆盖安装。' },
    { q: '为什么安装不了？', a: '目前只支持 64 位 ARM 的 Android 8.0 及以上手机。' },
  ],
};

const ios: Platform = {
  id: 'ios',
  name: 'iPhone',
  icon: 'phone',
  device: 'iOS 17 及以上，仅支持 iPhone',
  downloadKey: 'ios',
  sections: [
    {
      id: 'device',
      title: '适用设备',
      steps: [
        {
          title: '确认 iPhone 满足要求',
          body: '需要 iOS 17 及以上的 iPhone，暂不支持 iPad。查看方法：打开“设置”，进入“通用”，再点“关于本机”。',
        },
      ],
    },
    {
      id: 'download',
      title: '第一步 下载安装',
      steps: [
        {
          title: '用 Safari 打开下载页',
          body: '一定要用 iPhone 自带的 Safari 打开[下载页](/download)，用微信或其他浏览器打开可能没有反应。',
          download: true,
        },
        {
          title: '点“在 iPhone 上安装”',
          body: '系统询问是否安装时，选择“安装”，然后回到主屏幕，等待图标下载完成。',
        },
        {
          title: '遇到“未受信任”提示时',
          body: '首次打开如果提示“未受信任的开发者”，到“设置 → 通用 → VPN 与设备管理”，找到 AsterLink 并“信任该开发者”。',
        },
      ],
      note: {
        kind: 'warn',
        text: '仅限已登记的设备安装。如果点击后提示无法安装，请联系客服登记你的设备。',
      },
    },
    {
      id: 'authorize',
      title: '第二步 首次授权',
      steps: [
        {
          title: '点“继续”',
          body: '第一次点连接时，AsterLink 会先显示“首次连接需要添加 VPN 配置”，点“继续”。',
        },
        {
          title: '允许添加 VPN 配置',
          body: '系统弹窗“AsterLink 想要添加 VPN 配置”，选择“允许”。',
        },
        {
          title: '验证身份',
          body: '按提示输入锁屏密码或使用面容 ID。这一步只由系统处理，AsterLink 不会获取或保存它。',
        },
        {
          title: '允许通知',
          body: '连接成功后会询问通知权限，建议允许：套餐到期或流量用尽导致断开时会提醒你。',
        },
      ],
    },
    {
      id: 'login',
      title: '第三步 登录',
      steps: [
        { title: '输入邮箱和密码', body: '填写网站注册时的邮箱和密码，点“登录”。' },
        {
          title: '选择是否记住密码',
          body: '勾选“记住密码”，下次打开会自动登录。忘记密码点“忘记密码”。',
        },
      ],
      note: { kind: 'warn', text: '“我的”页的“退出登录”会断开连接，并删除记住的密码。' },
    },
    {
      id: 'lines',
      title: '第四步 选择线路',
      steps: [
        {
          title: '打开“线路”',
          body: '点底部的“线路”标签。可以搜索，也可以按“全部”“专线”“普通线路”筛选。',
        },
        { title: '点选一条线路', body: '点一条线路，它会出现已选标记。' },
        { title: '看懂线路倍率', body: lineExplain },
      ],
      note: { kind: 'tip', text: '已连接时换线路，会提示“将断开当前连接，切换后需要重新连接”。' },
    },
    {
      id: 'connect',
      title: '第五步 连接与断开',
      steps: [
        { title: '回到“连接”', body: '点底部的“连接”标签，页面中间是一个大圆形按钮。' },
        {
          title: '点圆形按钮连接',
          body: '状态会依次显示“正在准备”“正在连接”，成功后显示“已连接”和连接时长，下方显示“本次已用 X · 按倍率计 Y”。手机顶部状态栏会出现“VPN”图标。',
        },
        {
          title: '再点一次断开',
          body: '请在 AsterLink 里连接和断开。系统“设置”里也有 VPN 开关，但它不会按你选好的线路连接，不建议使用。',
        },
      ],
    },
    {
      id: 'usage',
      title: '第六步 查看套餐与用量',
      steps: [
        {
          title: '在“连接”页看套餐卡',
          body: '套餐卡上有剩余流量和到期时间。看到“正在同步用量”表示刚用的流量正在上报，稍等片刻就会更新。',
        },
        {
          title: '续费与升级',
          body: '套餐用完或到期后，到网站的[套餐与订阅](/plans)续费，回到 AsterLink 会自动同步。',
        },
        {
          title: '查看配置状态',
          body: '“我的”页可以看到 VPN 配置的状态，显示“尚未添加”表示还没有允许添加。',
        },
      ],
    },
    {
      id: 'background',
      title: '后台运行与通知',
      steps: [
        {
          title: '保持通知开启',
          body: '因套餐到期或流量用尽在后台断开时，AsterLink 会用通知提醒你。请在“设置 → 通知”中保持 AsterLink 的通知开启。',
        },
        {
          title: '切到其他应用',
          body: '连接后可以放心切到其他应用，只要状态栏还有“VPN”图标，连接就在继续。',
        },
      ],
    },
    {
      id: 'upgrade',
      title: '升级与卸载',
      steps: [
        {
          title: '升级',
          body: '回到[下载页](/download)再安装一次新版本即可覆盖，记住的密码与 VPN 配置会保留。',
        },
        {
          title: '卸载',
          body: '长按 AsterLink 图标，选择删除应用，VPN 配置会一并移除。',
        },
      ],
    },
  ],
  faq: [
    {
      q: '点“在 iPhone 上安装”没有反应怎么办？',
      a: '请改用 iPhone 自带的 Safari 打开下载页。在微信或其他浏览器里打开，系统不会弹出安装提示。',
    },
    {
      q: '拒绝了添加 VPN 配置怎么办？',
      a: '“我的”页的 VPN 配置会显示“尚未添加”。回到“连接”页再点一次圆形按钮，在系统弹窗里选择“允许”。',
    },
    { q: '可以装在 iPad 上吗？', a: '暂时不可以，目前只支持 iPhone。' },
  ],
};

export const platforms: Record<PlatformId, Platform> = { windows, macos, android, ios };

/** The questions every platform answers, with that platform's own button names. */
export function commonFaq(id: PlatformId): Faq[] {
  const mobile = id === 'android' || id === 'ios';
  const where = mobile ? '“我的”页' : '“账户”页';
  return [
    {
      q: '提示没有套餐、套餐已到期或流量用尽怎么办？',
      a: '到网站的[套餐与订阅](/plans)购买或续费，回到 AsterLink 会自动同步，不需要重新登录。',
    },
    {
      q: '提示“已达到同时在线设备上限”怎么办？',
      a: `先在其他设备上断开连接或退出登录，再回来重新连接。同时在线的设备数以套餐为准，可以在[总览](/dashboard)页查看。`,
    },
    {
      q: '连上了但打不开网页怎么办？',
      a: `先换一条线路再试。仍然不行，请在${where}点“复制诊断信息”，再到“我的工单”提交，把诊断信息粘贴进去。`,
    },
    {
      q: '忘记密码怎么办？',
      a: '在登录页点“忘记密码”，或直接打开网站的[找回密码](/forgot-password)页面。',
    },
    {
      q: '换了手机或电脑怎么办？',
      a: `在新设备上用同一个账号登录即可。旧设备请退出登录，释放设备名额。`,
    },
    {
      q: '为什么流量比预期用得快？',
      a: '线路有“线路倍率”，倍率 1.5× 表示用掉 1 GB 会按 1.5 GB 扣减。连接页的“按倍率计”显示的就是实际扣减的流量。',
    },
  ];
}

export function faqFor(id: PlatformId): Faq[] {
  return [...platforms[id].faq, ...commonFaq(id)];
}

// ---- inline text ----------------------------------------------------------------------------

export type Inline =
  | { type: 'text'; text: string }
  | { type: 'strong'; text: string }
  | { type: 'link'; text: string; href: string };

/** Splits a guide text into plain, emphasised and linked pieces. */
export function parseInline(text: string): Inline[] {
  const out: Inline[] = [];
  const pattern = /\*\*([^*]+)\*\*|\[([^\]]+)\]\(([^)]+)\)/g;
  let last = 0;
  for (const match of text.matchAll(pattern)) {
    if (match.index > last) out.push({ type: 'text', text: text.slice(last, match.index) });
    if (match[1] !== undefined) out.push({ type: 'strong', text: match[1] });
    else out.push({ type: 'link', text: match[2], href: match[3] });
    last = match.index + match[0].length;
  }
  if (last < text.length) out.push({ type: 'text', text: text.slice(last) });
  return out;
}

/** Every piece of visible text of a platform's guide (the copy checks read this). */
export function guideTexts(id: PlatformId): string[] {
  const platform = platforms[id];
  const texts = [platform.name, platform.device];
  for (const section of platform.sections) {
    texts.push(section.title);
    if (section.note) texts.push(section.note.text);
    for (const step of section.steps) texts.push(step.title, step.body);
  }
  for (const item of faqFor(id)) texts.push(item.q, item.a);
  return texts;
}

// ---- choosing the platform ------------------------------------------------------------------

export const isPlatformId = (value: string | null | undefined): value is PlatformId =>
  !!value && (platformIds as string[]).includes(value);

/**
 * The platform of a browser's User-Agent. An iPad's Safari reports a Mac, so it lands on macOS
 * (the iPad is not supported either way); anything unknown gets Windows.
 */
export function detectPlatform(userAgent: string): PlatformId {
  if (/iPhone|iPod/i.test(userAgent)) return 'ios';
  if (/Android/i.test(userAgent)) return 'android';
  if (/Macintosh|Mac OS X|iPad/i.test(userAgent)) return 'macos';
  return 'windows';
}

/** The query parameter wins; a missing or unknown value falls back to the visitor's device. */
export function choosePlatform(param: string | null | undefined, userAgent: string): PlatformId {
  return isPlatformId(param) ? param : detectPlatform(userAgent);
}

/** What the page knows about a released client (the version only; nothing else leaves the server). */
export type GuideDownloads = {
  android: { version: string } | null;
  ios: { version: string } | null;
};

export function downloadAvailable(id: PlatformId, downloads: GuideDownloads): boolean {
  const key = platforms[id].downloadKey;
  return key !== null && downloads[key] !== null;
}

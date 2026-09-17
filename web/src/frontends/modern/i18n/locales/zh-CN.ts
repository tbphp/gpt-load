import { protocolMessages as protocols } from '../protocols'
import { zhCN as inspector } from './inspector'
import { zhCN as home } from './home'
import { zhCN as accessKeys } from './access-keys'
import { zhCN as logs } from './logs'
import { zhCN as usage } from './usage'
import { zhCN as health } from './health'
import { zhCN as credentialCards } from './credential-cards'
import { zhCN as groupWorkflows } from './group-workflows'
import { zhCN as parameterRules } from './parameter-rules'
import { zhCN as groupDetail } from './group-detail'
import { zhCN as groupMessages } from './groups'
import { zhCN as ui } from './ui'
import { zhCN as groupCreate } from './group-create'
import { zhCN as modelSelection } from './model-selection'
import { zhCN as modelManager } from './model-manager'
import { zhCN as settingsForm } from './settings-form'
import { zhCN as subscriptions } from './subscriptions'

export default {
  home,
  inspector,
  protocols,
  logs,
  usage,
  health,
  accessKeys,
  groupWorkflows,
  parameterRules,
  credentialCards,
  groupDetail,
  subscriptions,
  modelSelection,
  modelManager,
  settingsForm,
  groupCreate,
  ui,
  ...groupMessages,
  sections: {
    workspace: '工作区',
    observe: '运行观测',
    system: '系统',
  },
  shell: {
    refresh: '刷新',
    refreshedAt: '更新于 {time}',
    importCredentials: '导入密钥',
    goHome: 'GPT-Load 总览',
    mascotHint: '长按吉祥物或聚焦后按空格与它互动，按回车返回总览。',
    collapseSidebar: '收起侧栏',
    expandSidebar: '展开侧栏',
    close: '关闭',
    documentation: '使用文档',
    sponsor: '赞助支持',
    mobileNavigationDescription: '直接进入管理工作区和运行观测页面。',
    navigationFailed: '无法加载页面，请重新加载。',
    reload: '重新加载',
  },
  auth: {
    title: '登录 GPT-Load',
    description: '输入管理员密钥或访问密钥，系统会识别对应权限。',
    keyLabel: '登录密钥',
    keyPlaceholder: '输入 AUTH_KEY 或访问密钥',
    reveal: '显示登录密钥',
    conceal: '隐藏登录密钥',
    submit: '登录',
    submitting: '正在验证…',
    required: '请输入登录密钥',
    invalidFormat: '登录密钥不能包含空白字符',
    invalid: '登录密钥无效，请检查后重试。',
    locked: '认证尝试过多，请在 {seconds} 秒后重试。',
    lockEnded: '可以重新验证会话了。',
    network: '无法连接到管理 API，请检查服务后重试。',
    invalidResponse: '管理 API 返回了无法识别的响应，请稍后重试。',
    restoreTitle: '验证登录状态',
    checking: '正在验证当前会话…',
    retry: '重新验证',
    changeKey: '更换密钥',
    signedIn: '登录成功，正在打开页面…',
    continue: '继续进入',
    remember: '记住登录',
    logout: '退出登录',
    readOnly: '访问密钥 · 只读',
    readOnlyDescription: '仅能查看此密钥的总览、模型、用量和请求记录，不能修改系统配置。',
    help: {
      title: '可以使用哪种密钥？',
      accessKeyTitle: '访问密钥',
      accessKey: '使用已分发的访问密钥，查看该密钥范围内的数据；不能修改配置。',
      adminTitle: '管理员密钥',
      admin: '如果实例设置了 AUTH_KEY，使用部署系统中的对应值。',
      file: '未设置 AUTH_KEY 时，密钥位于 {path}；容器默认路径为 {containerPath}。',
      docker: 'Docker 部署可在自己的终端进入容器后读取文件；不要把密钥贴到日志或聊天中。',
    },
  },
  pages: {
    home: { title: '总览' },
    groups: { title: '分组' },
    groupDetail: { title: '分组详情' },
    models: { title: '模型' },
    accessKeys: { title: '访问密钥' },
    usage: { title: '用量统计' },
    logs: { title: '请求日志' },
    health: { title: '运行健康' },
    inspector: { title: '路由检查' },
    settings: { title: '全局设置' },
    import: { title: '导入凭据' },
    login: { title: '登录' },
  },
  notFound: {
    title: '页面不存在',
    description: '请检查页面地址，或返回总览。',
    backHome: '返回总览',
  },
  appearance: {
    theme: '显示模式',
    language: '界面语言',
    themes: {
      system: '跟随系统',
      light: '浅色',
      dark: '深色',
    },
    persistenceFailed: '浏览器未允许保存偏好，本次选择在当前访问中有效。',
  },
  system: {
    currentVersion: '当前版本：{version}',
    loadingVersion: '加载中…',
    versionUnavailable: '版本未知',
    checkUpdate: '检查更新',
    checking: '检查中…',
    latestVersion: '已是最新版本',
    updateAvailable: '新版本 {version}',
    checkFailed: '检查失败，请重试',
    authRequired: '需要管理员登录',
  },
  navigation: '主导航',
  skipToContent: '跳到主要内容',
  interfaceSettings: '界面设置',
  frontend: {
    description: '切换后重新加载，仅影响当前浏览器。',
    current: '当前界面',
    saveFailed: '无法保存界面偏好，请允许本站使用浏览器存储后重试。',
    modern: { title: '现代版' },
    classic: { title: '经典版' },
  },
}

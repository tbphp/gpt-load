export default {
  sections: {
    workspace: '工作区',
    observe: '运行观测',
    system: '系统',
  },
  shell: {
    goHome: 'GPT-Load 总览',
    collapseSidebar: '收起侧栏',
    expandSidebar: '展开侧栏',
    close: '关闭',
    documentation: '使用文档',
    selfHosted: '自托管 AI 网关',
    mobileNavigationDescription: '直接进入管理工作区和运行观测页面。',
    preview: '框架预览',
    previewDescription: '业务数据尚未接入，完整管理功能可在经典版使用。',
  },
  pages: {
    home: {
      title: '总览',
      description: '从资源配置到运行观测，集中管理你的 AI 网关。',
    },
    groups: {
      title: '分组',
      description: '管理上游连接、凭据与模型，直接处理分组配置。',
    },
    models: {
      title: '模型',
      description: '查看模型目录、路由能力和价格配置。',
    },
    accessKeys: {
      title: '访问密钥',
      description: '管理客户端的访问入口、使用范围和额度。',
    },
    usage: {
      title: '用量统计',
      description: '查看请求用量、Token 消耗与预估费用。',
    },
    logs: {
      title: '请求日志',
      description: '追踪请求结果、上游尝试和用量明细。',
    },
    health: {
      title: '运行健康',
      description: '查看凭据可用性、冷却状态与运行问题。',
    },
    inspector: {
      title: '路由检查',
      description: '解释模型如何匹配分组和上游凭据。',
    },
    settings: {
      title: '全局设置',
      description: '调整当前浏览器的外观、语言和界面偏好。',
    },
  },
  home: {
    manageGroups: '管理分组',
    workspace: {
      title: '资源与访问',
      description: '配置上游能力，管理客户端入口。',
    },
    observe: {
      title: '运行观测',
      description: '从使用情况到具体请求，直接进入所需视图。',
    },
    workflowTitle: '接入流程',
    workflowDescription: '连接上游，再向客户端提供统一入口。',
    workflowGroups: '创建分组',
    workflowCredentials: '配置凭据与模型',
    workflowAccess: '创建访问密钥',
  },
  workspace: {
    pendingTitle: '此工作区正在建设中',
    pendingDescription: '页面入口已就绪，业务数据与具体操作将在后续逐步接入。',
    backHome: '返回总览',
  },
  appearance: {
    title: '外观与偏好',
    description: '仅影响当前浏览器，不改变网关的运行配置。',
    theme: '显示模式',
    themeDescription: '选择明暗外观，或自动跟随系统。',
    language: '界面语言',
    languageDescription: '切换后立即生效。',
    themes: {
      system: '跟随系统',
      light: '浅色',
      dark: '深色',
    },
    persistenceFailed: '浏览器未允许保存偏好，本次选择在当前访问中有效。',
  },
  quickNavigation: {
    title: '快速跳转',
    placeholder: '搜索页面与功能…',
    description: '搜索页面名称，使用方向键选择并按回车进入。',
    pages: '页面与功能',
    empty: '没有找到匹配的页面。',
    keyboardHint: '↑ ↓ 选择　Enter 进入　Esc 关闭',
  },
  navigation: '主导航',
  skipToContent: '跳到主要内容',
  settings: '全局设置',
  interfaceSettings: '界面设置',
  homeTitle: '新版界面',
  homeDescription: '新版界面正在完善，完整管理功能目前可在经典版使用。',
  unavailableTitle: '此页面暂未在新版提供',
  frontend: {
    description: '选择当前浏览器使用的界面。切换后会重新加载，不影响其他访问者。',
    current: '当前界面',
    previewNote: '缩略图为布局示意。新版预览将在正式设计确定后更新。',
    saveFailed: '无法保存界面偏好，请允许本站使用浏览器存储后重试。',
    modern: { title: '新版', description: '正在建设中的全新管理界面。' },
    classic: { title: '经典版', description: '使用现有布局和完整管理功能。' },
  },
}

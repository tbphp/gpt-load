# 新版前端开发约定

本约定适用于 `modern` 的页面、业务功能和公共组件。界面采用亮橙红、白色与中性灰；新页面遵循同一套视觉基础，不另外选择主题、控件尺寸或交互库。

原视觉规范：[GPT-Load 新版前端视觉与组件规范（Coral）](https://app.notion.com/p/3d75e49ce6ae8148bbe5c0486a3ba9e7)。配色以当前 `styles/tokens.css` 为准：主操作使用亮橙红 `#FF4F1F`，选择态使用浅橙底色，灰阶不带紫色调；Logo 保留原品牌素材颜色。本文维护工程入口、组件用法与开发约束。

## 代码职责

| 位置                  | 职责                                                         |
| --------------------- | ------------------------------------------------------------ |
| `styles/tokens.css`   | 颜色、字体、间距、圆角、控件尺寸、图标、层级、动效与布局变量 |
| `styles/base.css`     | 全局重置、基础排版、焦点、读屏、减少动效；不放组件样式       |
| `components/ui/`      | 无业务依赖的基础控件，通过 props、slots、events 组合         |
| `components/`         | 全站复用的页头、品牌与提示组件                               |
| `layouts/`            | 导航、页头、移动侧栏等应用布局                               |
| `features/<feature>/` | 业务页面、展示模型、功能专用组件及查询编排                   |
| `api/`                | 新版自己的接口与响应类型；不借用旧版页面模型                 |
| `app/`                | 应用级导航、偏好与响应断点                                   |

公共组件不能导入 `features`、`layouts`、`api`、`app` 或 `shared/http`，也不自行读取登录态或发送业务请求。业务数据与操作通过 props / events 交给组件。公共组件跨页面复用，但新旧前端之间不复用页面、样式或业务组件。

## 样式写法

- 所有颜色、字号、字重、行高、间距、圆角、阴影、边框、层级和动效时长使用 `--modern-*` 变量。根据用途选语义变量，不按“看起来相同”复制某个色值。
- 普通文字使用 `text` / `muted`，当前导航和选择态使用 `accent` / `accent-soft`，主操作使用 `action` / `on-action`；`coral` 只作品牌装饰。错误、警告、成功与信息分别使用对应的语义颜色，不把品牌色当成错误色。
- `tokens.css` 同时维护浅色与深色值。业务组件不自行查询系统主题，不另写一套深色调色板。浮层挂到 body，使用根级视觉变量。
- 组件样式与组件同文件，使用 `<style scoped>` 和 `modern-` 类名前缀。通过 Portal / Teleport 挂到页面根部的浮层节点没有组件 scope 属性，其组件可以使用普通 `<style>`，但选择器必须是该公共组件独占的 `modern-` 类名。页面只写自己的布局和业务差异，不通过覆盖 `.modern-button` 等公共组件内部类名定制外观；优先使用组件 props / slots。
- CSS 禁止静态内联样式。动态样式仅用于数据决定的比例、位置等值；不能借 `:style` 硬编码颜色、字号或间距。
- 百分比、视口单位、`auto`、零值以及图片比例不需要建变量。只出现一次的布局几何值（如预览卡片宽度、命令列表高度）可留在对应组件；相同语义开始被复用时再收敛，不为每个像素创建全局变量。
- CSS 媒体条件无法引用自定义属性，因此保留约定的字面量阈值；它们由 `check:styles` 对照 `app/breakpoints.ts` 检查。JS 直接导入断点或查询，不能再手写一套数值。

```css
.feature-layout {
  display: grid;
  gap: var(--modern-space-4);
  color: var(--modern-text);
}
```

## 优先复用的组件

| 组件                       | 用法与边界                                                                                                      |
| -------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `PageHeader`               | 登录页等公共布局里的标题块；业务页标题由顶栏统一渲染，页面最外层使用 `modern-page`                              |
| `AppPanel`                 | 有标题的内容面板，统一标题、说明、内边距；`actions` 插槽放面板操作                                              |
| `AppButton`                | 标准按钮，`variant` 为 default / primary / ghost / brand / danger，`size` 为 xs / sm / md；统一禁用、加载和焦点 |
| `AppIconButton`            | 纯图标按钮，必须提供 `label`；不在页面里重复实现 aria-label、尺寸和加载状态                                     |
| `AppTextField`             | 文本输入、label、错误和焦点样式；通过 v-model 绑定，原生 input 属性透传，suffix 插槽放辅助操作                  |
| `AppCheckbox`              | 布尔选项，通过 v-model 绑定；必须提供 label，支持禁用，原生 input 属性透传                                      |
| `AppSwitch`                | 即时布尔操作，通过 modelValue / update:modelValue 受控，loading 期间禁止重复操作                                |
| `AppSelect`                | 带标签的原生单选框，支持隐藏可见标签；筛选与表单统一使用                                                        |
| `AppCollectionState`       | 列表加载、空数据和失败状态，可插入重试或创建操作                                                                |
| `AppBadge`                 | 非交互身份或状态标签，可选图标，neutral / info 语义配色；不承担按钮或菜单行为                                   |
| `AppIcon`                  | 普通图标统一来自 Lucide，使用 xs / sm / md / lg 命名尺寸；品牌图标保留官方 SVG 路径                             |
| `AppSelectMenu`            | 图标触发的单选菜单，通过 modelValue / options 传值；现有主题、语言选择共用                                      |
| `AppDialogContent`         | 在 Reka `DialogRoot` 内使用，统一 Portal、遮罩、层级、可访问标题和描述；dialog / sidebar 两种位置               |
| `HintTooltip`              | 需要统一外观与延迟的提示一律用它；有可见同名文字时禁用，折叠侧栏的无文字图标可启用                              |
| `AppNotice`                | 信息、成功、警告和错误提示，自动设置 status / alert；可选边框及 actions 插槽                                    |
| `AppExternalLink`          | 统一新窗口外链的 target / rel；调用方提供链接内容和必要的无障碍名称                                             |
| `BrandLogo` / `GitHubIcon` | 已确认的品牌素材，不能临时绘制或用其他通用图标代替                                                              |

提示分两层：`AppIconButton` 自带的原生 `title` 只作为无脚本时的兜底名称，需要与设计一致的悬浮提示统一使用 `HintTooltip`。同一元素不要同时挂两者，否则折叠侧栏等场景会叠出两个气泡。

普通按钮默认 `type="button"`。图标按钮不要再包一层 Tooltip 充当菜单触发器；下拉菜单使用 `AppSelectMenu`。需要把公共按钮样式用于路由链接时，使用 `as-child`，保留链接的真实语义：

```vue
<AppButton as-child>
  <RouterLink :to="{ name: 'modern-home' }">
    <AppIcon :icon="ArrowLeft" size="sm" />{{ t('notFound.backHome') }}
  </RouterLink>
</AppButton>
```

`as-child` 必须提供一个实际可交互的元素/组件，内容与图标由子元素提供；加载或禁用时仍阻止点击并设置 aria 状态。不要用 `div` 冒充按钮或链接。

对话框保留 Reka 的 Root / Trigger / Close 负责打开、关闭、焦点和键盘行为，内容容器统一复用，避免手写另一套遮罩、Portal 或焦点锁：

```vue
<DialogRoot v-model:open="open">
  <DialogTrigger as-child>
    <AppIconButton :icon="Menu" :label="t('navigation')" />
  </DialogTrigger>
  <AppDialogContent :title="title" :description="description">
    <!-- 功能内容；关闭操作使用 DialogClose as-child 配合 AppIconButton -->
  </AppDialogContent>
</DialogRoot>
```

## 复用与交互原则

1. 新页面先查上面的组件目录和既有功能，再决定是否新增组件。通用交互一出现就使用基础组件；相同业务展示被两个位置复用时，提取到合适的公共位置。
2. 抽象以共同语义为依据，不只因为两段 HTML 相似。通过少量明确的 props 和 slots 组合，不增加万能表单、页面生成器、无实际用途的配置层。
3. 命令搜索选项、界面预览选择卡、侧栏分隔线折叠控件是专门交互，可保留专门结构；它们仍必须使用统一视觉变量和图标。不要为了套入普通按钮而丢失语义。
4. 常用操作优先放在当前页、行内或直接可达的位置。新增 Tab 前评估操作层级、相关信息同时可见性和上下文切换成本，不能默认通过嵌套 Tab 组织所有内容。
5. 可见文案通过 i18n，新增键同步中文、英文、日文。长文本可换行或截断，操作不能被挤出容器；错误、空状态与加载状态不得用虚构数据替代。
6. 键盘焦点必须可见，图标按钮有明确名称；不只用颜色传达状态。异步动作避免重复提交。减少动效由全局规则处理，组件不自行覆盖。
7. 宽工作区使用全局内容内边距，不增加居中的固定最大宽容器。移动端控件使用统一触摸尺寸，表格等复杂内容在自己的区域处理滚动。
8. 顶栏是所有页面共用的固定结构，由 `AppLayout` 渲染：左侧当前页标题，接着是刷新时间与刷新按钮，一条竖线之后是导入密钥、明暗、语言和退出。业务页不自行渲染标题，也不要往顶栏加东西；需要刷新的页面用 `app/page-refresh.ts` 的 `usePageRefresh` 登记 `refresh` / `pending` / `updatedAt`，顶栏据此显示。页面自己的主操作放在页内工具条最右侧，搜索与筛选排在它左边；页内吸顶元素按 `top: var(--modern-topbar-height)` 计算。
9. 滚动由页面承担：侧栏 `sticky` 并在自身内部滚动，顶栏 `sticky` 吸顶，内容区不另建滚动容器。独立滚动区只隔离实际滚动的轴：横向容器使用 `overscroll-behavior-x: contain`，不能用双向 contain 截断页面纵向滚动。滚动条粗细与配色由 `base.css` 统一；浮层打开时的宽度补偿由 Reka 的 body 滚动锁负责，不叠加 `scrollbar-gutter`。

## 认证与页面接入

页面标题由 `app/use-page-title.ts` 统一推导（`meta.titleKey` → 导航项标题 → 未找到），文档标题、面包屑与路由播报共用这一份，路由不再通过 props 重复传标题键。受保护页面由 `AuthGate` 在身份验证成功后挂载；页面不得用浏览器是否存在密钥代替服务端认证。菜单权限在 `app/navigation.ts` 中声明，路由、侧栏、快捷导航使用同一规则。业务查询从共享注入入口 `useApiClient()` 取得新版会话客户端；不要自行创建绕过会话清理的 HTTP 客户端或从存储读取密钥。共享客户端只受理 `/api/` 路径，`/health` 这类无需认证的公开端点在 `api/` 内直接 `fetch` 并同样校验响应，除此之外不得绕过会话客户端。

`features/auth/` 独立维护认证流程，`app/api-client.ts` 负责阻止旧身份的失败响应清除新会话。所有新版查询共用 bootstrap 装配的 QueryClient，以便退出或换身份时统一取消与清理。功能自己的在途请求和临时敏感状态仍须在卸载时清理，后续编辑与导入页面接入时补充对应恢复与未保存保护。

分组工作区通过 `GET /api/modern/groups` 读取完整的轻量展示快照，包括服务原因、配置摘要与最近活跃小时。该端点使用原有只读采集，保留经典版接口的分页和字段合同。前端本地搜索、筛选和排序，条件写入 URL；首批渲染 60 个卡片，更多分组通过“继续显示”追加，不分割成独立页。展开卡片就地按需读取模型目录，暂停的分组不展示误导性的健康比例。活跃信息是小时聚合，不标为精确请求时间。

启停与基础编辑使用原有设置接口，只提交修改字段。编辑面板管理名称、权重、价格倍率与启停，保留未保存保护；高级配置、凭据写入和导入页面后续独立实现。右侧编辑面板复用 `AppDialogContent` 的 `editor` 位置。

## 检查与修改流程

- 修改公共变量或组件时，查看所有调用位置，确认语义与状态保持一致；不能只调整一个页面的覆盖样式。
- `pnpm run lint` 已包含 `check:styles`：检查硬编码视觉值、未定义变量及响应断点，并限制业务页面绕过公共浮层组件。
- ESLint 同时检查公共组件的依赖方向和新旧前端边界。
- 类型、格式、构建沿用现有命令，最终运行 `make check`。不增加前端测试或浏览器验收，不运行本地 race。
- 新依赖、后端接口或核心流程不属于视觉基础调整，按实际业务需求另行处理。

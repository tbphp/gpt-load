# 新版前端开发约定

本约定适用于 `modern` 的页面、业务功能和公共组件。保留已确认的 Coral / Ink / Cream 框架；新页面遵循同一套视觉基础，不另外选择主题、控件尺寸或交互库。

正式视觉规范：[GPT-Load 新版前端视觉与组件规范（Coral）](https://app.notion.com/p/3d75e49ce6ae8148bbe5c0486a3ba9e7)。本文只维护工程入口、组件用法与开发约束。

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
- 组件样式与组件同文件，使用 `<style scoped>` 和 `modern-` 类名前缀。页面只写自己的布局和业务差异，不通过覆盖 `.modern-button` 等公共组件内部类名定制外观；优先使用组件 props / slots。
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

| 组件                       | 用法与边界                                                                                              |
| -------------------------- | ------------------------------------------------------------------------------------------------------- |
| `PageHeader`               | 页面标题、可选说明、默认插槽放页面主操作；页面最外层使用 `modern-page`                                  |
| `AppPanel`                 | 有标题的内容面板，统一标题、说明、内边距；`actions` 插槽放面板操作                                      |
| `AppButton`                | 标准按钮，`variant` 为 default / primary / ghost / danger，`size` 为 xs / sm / md；统一禁用、加载和焦点 |
| `AppIconButton`            | 纯图标按钮，必须提供 `label`；不在页面里重复实现 aria-label、尺寸和加载状态                             |
| `AppIcon`                  | 普通图标统一来自 Lucide，使用 xs / sm / md / lg 命名尺寸；品牌图标保留官方 SVG 路径                     |
| `AppSelectMenu`            | 图标触发的单选菜单，通过 modelValue / options 传值；现有主题、语言选择共用                              |
| `AppDialogContent`         | 在 Reka `DialogRoot` 内使用，统一 Portal、遮罩、层级、可访问标题和描述；dialog / sidebar 两种位置       |
| `HintTooltip`              | 只补充必要信息；有可见同名文字时禁用，折叠侧栏的无文字图标可启用                                        |
| `AppNotice`                | 信息、成功、警告和错误提示，自动设置 status / alert；可选边框及 actions 插槽                            |
| `AppExternalLink`          | 统一新窗口外链的 target / rel；调用方提供链接内容和必要的无障碍名称                                     |
| `BrandLogo` / `GitHubIcon` | 已确认的品牌素材，不能临时绘制或用其他通用图标代替                                                      |

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
    <AppIconButton :icon="Search" :label="t('quickNavigation.title')" />
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

## 检查与修改流程

- 修改公共变量或组件时，查看所有调用位置，确认语义与状态保持一致；不能只调整一个页面的覆盖样式。
- `pnpm run lint` 已包含 `check:styles`：检查硬编码视觉值、未定义变量及响应断点，并限制业务页面绕过公共浮层组件。
- ESLint 同时检查公共组件的依赖方向和新旧前端边界。
- 类型、格式、构建沿用现有命令，最终运行 `make check`。不增加前端测试或浏览器验收，不运行本地 race。
- 新依赖、后端接口或核心流程不属于视觉基础调整，按实际业务需求另行处理。

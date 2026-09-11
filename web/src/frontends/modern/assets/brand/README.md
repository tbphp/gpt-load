# 新版品牌资源

来自用户确认的 GPT-Load Coral Anchor 资源包，以下 SVG 均原样复制，未修改路径、颜色或比例：

- `logo-light.svg`：原 `svg/logo-horizontal-color.svg`，浅色背景使用。
- `logo-dark.svg`：原 `svg/logo-horizontal-dark.svg`，深色背景使用。
- `icon.svg`：原 `web/icon.svg`，用于收起侧栏。

素材品牌色为 Coral `#DB7458`、Ink `#282630`、Cream `#FAF7F0`。横版 Logo 保持 4:1 比例，窄位置使用专门的方形图标。界面配色独立维护于 `styles/tokens.css`，主按钮与装饰使用亮橙红 `#FF4F1F`，不从 Logo 取色后加深。

Logo 仅由 modern 引用，旧版页面中的品牌展示保持不变。网站图标 `web/public/favicon.svg` 复用原始吉祥物路径，使用 Coral 填色和透明背景，等比放大以适应浏览器标签页的小尺寸。由 `web/index.html` 直接声明，新旧界面和登录页共用，不依赖前端启动后替换。图标 URL 带素材版本，更新素材时同步更新版本以刷新浏览器缓存。

`components/GitHubIcon.vue` 使用 GitHub 官方 [Octicons 的 mark-github-16](https://github.com/primer/octicons/blob/main/icons/mark-github-16.svg)，保留原始路径，以 `currentColor` 适配明暗主题。其 MIT 许可证见本目录的 `octicons.LICENSE`。

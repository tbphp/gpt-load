# 新版品牌资源

来自用户确认的 GPT-Load Coral Anchor 资源包，保留原始图形、文字路径和比例，配色与新版界面保持一致：

- `logo-light.svg`：原 `svg/logo-horizontal-color.svg`，浅色背景使用。
- `logo-dark.svg`：原 `svg/logo-horizontal-dark.svg`，深色背景使用。
- `icon.svg`：原 `web/icon.svg`，用于收起侧栏。

图形使用品牌橘红 `#FF4F1F`；浅色背景的文字使用中性深灰 `#1C1C1B`，深色背景的文字使用近白色 `#F5F5F2`。横版 Logo 保持 4:1 比例，窄位置使用橘红底、近白图形的方形图标。界面配色仍由 `styles/tokens.css` 独立维护。

这些资源仅由 modern 引用。classic 的 `BrandMark.vue` 使用相同吉祥物路径，保持无文字、透明背景和原有方形尺寸，通过 `currentColor` 沿用旧版的 `--color-action`，适配浅色和深色主题。

网站图标 `web/public/favicon.svg` 使用同一吉祥物路径、品牌橘红填色和透明背景，等比放大以适应浏览器标签页的小尺寸。由 `web/index.html` 直接声明，新旧界面和登录页共用，不依赖前端启动后替换。图标 URL 带素材版本，更新素材时同步更新版本以刷新浏览器缓存。

`components/GitHubIcon.vue` 使用 GitHub 官方 [Octicons 的 mark-github-16](https://github.com/primer/octicons/blob/main/icons/mark-github-16.svg)，保留原始路径，以 `currentColor` 适配明暗主题。其 MIT 许可证见本目录的 `octicons.LICENSE`。

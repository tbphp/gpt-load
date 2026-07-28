# GPT-Load 2.0 accessibility evidence matrix

This matrix separates reproducible automated checks from genuine human browser and assistive-technology evidence. It is bound to source commit `c28dc80a725d2254f82898357a87a7dbe4b2d8d0`.

| Environment           | Mode                      |   Result | Version                                         | Evidence                                                            |
| --------------------- | ------------------------- | -------: | ----------------------------------------------- | ------------------------------------------------------------------- |
| Linux arm64 Chromium  | Automated                 |     PASS | Ubuntu 24.04; Chromium 149.0.7827.55 (rev 1228) | [runner evidence](artifacts/visual-runner-functional-chromium.json) |
| Linux arm64 WebKit    | Automated                 |     PASS | Ubuntu 24.04; WebKit 26.5 (rev 2311)            | [runner evidence](artifacts/visual-runner-functional-webkit.json)   |
| macOS Chrome          | Human geometry/functional |  NOT RUN | macOS 26.6; Chrome 150.0.7871.187               | No human session                                                    |
| macOS Safari          | Human geometry/functional |  NOT RUN | macOS 26.6; Safari 26.6                         | No human session                                                    |
| Safari + VoiceOver    | Human AT                  |  NOT RUN | macOS/Safari 26.6; built-in VoiceOver           | No human AT session                                                 |
| Windows Chrome + NVDA | External AT               | EXTERNAL | Environment unavailable                         | External release gate                                               |

The automated rows cover the embedded critical journey plus skip link, one H1, route focus/announcement, focus visibility, 44×44 targets, dialog focus/Escape/restore, keyboard scrolling, status text and icon, field relationships, copy live feedback, reduced motion, four viewport widths, two locales, and both themes.

Automated DOM and keyboard assertions do not establish Safari/VoiceOver or Windows/NVDA usability. Those rows remain explicitly unpassed.

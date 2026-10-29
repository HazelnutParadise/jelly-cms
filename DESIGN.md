---
version: alpha
name: Jelly CMS
description: 內容管理後台與帳號頁面的既有視覺規則
colors:
  primary: "#1a1d29"
  secondary: "#4ecdc4"
  tertiary: "#ff6b6b"
  neutral: "#f5f7fa"
typography:
  h1:
    fontFamily: Inter
    fontSize: 34.4px
    fontWeight: 700
    lineHeight: 1.2
    letterSpacing: -0.03em
  h2:
    fontFamily: Inter
    fontSize: 19.2px
    fontWeight: 700
    lineHeight: 1.35
  body-md:
    fontFamily: Inter
    fontSize: 16px
    fontWeight: 400
    lineHeight: 1.6
rounded:
  sm: 8px
  md: 12px
  full: 999px
spacing:
  sm: 8px
  md: 16px
  lg: 24px
  xl: 32px
components:
  button-primary:
    backgroundColor: "{colors.tertiary}"
    textColor: "{colors.primary}"
    rounded: "{rounded.sm}"
    padding: 10.4px 16px
  card:
    backgroundColor: "#ffffff"
    rounded: "{rounded.md}"
    padding: 24px
---

## Overview

Jelly CMS 供網站作者與管理員維護內容。後台使用 Bricolage Grotesque 標題字、Noto Sans TC 內文、粉紅品牌裝飾色與較深的實心操作色，按鈕和側欄延續 8／12px 對角圓角。本人帳號與復原頁共用較精簡的頁首，不加入後台側欄，並保留該區既有的 Inter、珊瑚紅及 8px 圓角規則。這份規則記錄目前實作，不表示其他既有頁面已完成無障礙、手機或深色驗收。

## Admin visual tokens

後台 `--primary` `#E8285B` 用於斜線、品牌圖案等裝飾；需要白字的選取側欄、主要按鈕、管理員頭像與通知數字使用 `--primary-solid` `#D92153`。兩色只改變用途，不改既有版面或亮暗模式。後台標題字使用自帶的 Bricolage Grotesque，內文使用自帶的 Noto Sans TC；圓角沿用對角單角形狀，小型控制項為 8px、卡片為 12px。

後台輸入提示文字使用不透明的 muted 色，空白編輯器標題也沿用相同規則。手機表格標題欄至少 12rem，並在表格容器內橫向捲動，以保留狀態、日期與操作欄位。

## Account page colors

珊瑚紅 `#ff6b6b` 用於主要操作，文字使用深色 `#1a1d29`，避免白字對比不足。藍綠色 `#4ecdc4` 保留品牌辨識。淺色頁面背景沿用 `#f5f7fa`，深色沿用 `#1a1d29`。

帳號頁的語意色彩依 `prefers-color-scheme` 切換，單頁使用同一模式。

| 用途 | 淺色 | 深色 |
|---|---|---|
| 頁面背景 | `#f5f7fa` | `#1a1d29` |
| 內容表面 | `#ffffff` | `#252938` |
| 文字 | `#1a1d29` | `#f0f2f7` |
| 說明文字 | `#52596a` | `#c0c5d1` |
| 邊框 | `#c8ced8` | `#555c6d` |
| 連結 | `#00635e` | `#76ded6` |
| 焦點框 | `#00635e` | `#4ecdc4` |
| 錯誤文字 | `#a2232c` | `#ff9b9f` |

帳號與復原頁的白色內容表面沿用既有後台。第三方標誌的白色底與官方原色依其品牌指引保留，是這些頁面配色的局部例外。官方來源與素材雜湊見 [品牌素材](docs/design/brand-spec.md)。

## Account page typography

沿用 Inter 與系統無襯線備援字。帳號頁不另載入外部字型，使用使用者已有的字型。主標題以 clamp 在 27.2 至 34.4px 間調整，內文 16px，次標題 19.2px。提供者名稱與狀態保持可掃讀，不用大寫裝飾標籤或版本標籤。

## Account page layout

帳號頁內容最大寬度 760px，左右保留至少 12 至 16px。提供者列在桌面將操作放右側，520px 以下改為單欄與整列按鈕。360px 與 1280px 都須驗證內容、焦點及操作不被裁切。

## Account page elevation & depth

帳號頁以表面色及一像素邊框區分內容，沒有裝飾陰影、發光或漸層背景。Google 原始標誌內的官方漸層保留。

## Account page shapes

內容區塊使用 12px 圓角，按鈕、輸入與提供者列使用 8px。狀態標籤採小型膠囊形，但不靠顏色單獨傳達狀態。

## Account page components

主要按鈕最小高度 44px，單行標籤。表單標籤放輸入上方，錯誤就近顯示。焦點框 3px 並保留外距。按下狀態只平移 1px，沒有持續動畫。

載入骨架對應三種登入方式，完成後必須移除。隱藏元件不能因 display 樣式而重新出現。操作期間禁重複送出，失敗保留可重試狀態。解除使用行內確認，復原碼只留在表單記憶體與 POST 本文。

## Do's and Don'ts

- 沿用既有品牌與模板，不另建前端框架。
- 繁中與英文使用各自完整文案，不把兩語永久並排。
- 兩種模式均檢查文字、按鈕、輸入與焦點對比。
- 不加入假帳號、假狀態、裝飾統計或非必要圖像。
- 品牌圖案使用官方原始素材，不以表情符號代替。
- 頁面通過真實瀏覽器操作、錯誤／離線、鍵盤與 Lighthouse 檢查後才可標示完成。

## 前台預設主題

前台以 `web/themes/default/layout.html` 與 `design/project/` 的既有板為準，使用 Bricolage Grotesque 標題字與 Noto Sans TC 內文，兩套字型均由 `/admin/assets/fonts/` 提供。前台的白底墨字、三色斜線、果凍丁與對角圓角沿用既有設計，與後台的色彩規則分開。

主要操作的白字背景以主色 95% 混入墨色，首頁藍色統計格同樣以第二色 95% 混入墨色。青色與黃色區塊的深字色以區塊色 25% 混入墨色，搜尋高亮文字明訂使用目前模式的墨色。預設色的亮、暗模式均以一般文字對比至少 4.5:1 驗收，保留主題自訂色設定。

首頁在桌面與頁首相距 16px，手機為 0；單篇與搜尋標題區緊貼頁首。手機首頁只保留 44px 標題、一行標語與疊在右上角的果凍丁。文章導覽依上一篇、下一篇各自的類名決定位置與底色；頁首斜線只由一個元素繪製。

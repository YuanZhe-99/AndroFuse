package gui

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
)

//go:embed assets/appicon.png
var appIconPNG []byte

//go:embed assets/tray.png
var trayLightPNG []byte

//go:embed assets/tray-black.png
var trayDarkPNG []byte

var appIconBitmap, _ = ui.DecodeBitmap(appIconPNG)

func lucide(inner string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + inner + `</svg>`))
}

var (
	iconDevices     = lucide(`<rect width="14" height="20" x="5" y="2" rx="2" ry="2"/><path d="M12 18h.01"/>`)
	iconModes       = lucide(`<rect width="7" height="7" x="3" y="3" rx="1"/><rect width="7" height="7" x="14" y="3" rx="1"/><rect width="7" height="7" x="14" y="14" rx="1"/><rect width="7" height="7" x="3" y="14" rx="1"/>`)
	iconDiagnostics = lucide(`<path d="M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2"/>`)
	iconSettings    = lucide(`<path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>`)
	iconInfo        = lucide(`<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>`)
	iconRefresh     = lucide(`<path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M8 16H3v5"/>`)
	iconShield      = lucide(`<path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/>`)

	winMinimize = lucide(`<path d="M5 12h14"/>`)
	winMaximize = lucide(`<rect width="14" height="14" x="5" y="5" rx="2"/>`)
	winRestore  = lucide(`<rect width="11" height="11" x="4" y="9" rx="1.5"/><path d="M9 9V6a2 2 0 0 1 2-2h7a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2h-3"/>`)
	winClose    = lucide(`<path d="M18 6 6 18"/><path d="m6 6 12 12"/>`)

	iconWifi         = lucide(`<path d="M12 20h.01"/><path d="M2 8.82a15 15 0 0 1 20 0"/><path d="M5 12.859a10 10 0 0 1 14 0"/><path d="M8.5 16.429a5 5 0 0 1 7 0"/>`)
	iconUsb          = lucide(`<circle cx="10" cy="7" r="1"/><circle cx="4" cy="20" r="1"/><path d="M4.7 19.3 19 5"/><path d="m21 3-3 1 2 2Z"/><path d="M9.2 7.4 16 14"/><path d="m11 19 6-6"/>`)
	iconDex          = lucide(`<rect width="20" height="14" x="2" y="3" rx="2"/><line x1="8" x2="16" y1="21" y2="21"/><line x1="12" x2="12" y1="17" y2="21"/>`)
	iconApps         = lucide(`<rect width="7" height="7" x="3" y="3" rx="1"/><rect width="7" height="7" x="14" y="3" rx="1"/><rect width="7" height="7" x="3" y="14" rx="1"/><path d="M14 17.5h7"/><path d="M17.5 14v7"/>`)
	iconBack         = lucide(`<path d="m15 18-6-6 6-6"/>`)
	iconHome         = lucide(`<path d="M15 21v-8a1 1 0 0 0-1-1h-4a1 1 0 0 0-1 1v8"/><path d="M3 10a2 2 0 0 1 .709-1.528l7-5.999a2 2 0 0 1 2.582 0l7 5.999A2 2 0 0 1 21 10v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>`)
	iconRecents      = lucide(`<line x1="6" x2="6" y1="5" y2="19"/><line x1="12" x2="12" y1="5" y2="19"/><line x1="18" x2="18" y1="5" y2="19"/>`)
	iconRotate       = lucide(`<path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/>`)
	iconScreenOff    = lucide(`<path d="M10.733 5.076a10.744 10.744 0 0 1 11.205 6.575 1 1 0 0 1 0 .696 10.747 10.747 0 0 1-1.444 2.49"/><path d="M14.084 14.158a3 3 0 0 1-4.242-4.242"/><path d="M17.479 17.499a10.75 10.75 0 0 1-15.417-5.151 1 1 0 0 1 0-.696 10.75 10.75 0 0 1 4.446-5.143"/><path d="m2 2 20 20"/>`)
	iconFullscreen   = lucide(`<path d="M8 3H5a2 2 0 0 0-2 2v3"/><path d="M21 8V5a2 2 0 0 0-2-2h-3"/><path d="M3 16v3a2 2 0 0 0 2 2h3"/><path d="M16 21h3a2 2 0 0 0 2-2v-3"/>`)
	iconCamera       = lucide(`<path d="M14.5 4h-5L7 7H4a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2h-3l-2.5-3z"/><circle cx="12" cy="13" r="3"/>`)
	iconCameraSwitch = lucide(`<path d="M11 19H4a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2h5"/><path d="M13 5h7a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2h-5"/><circle cx="12" cy="12" r="3"/><path d="m18 22-3-3 3-3"/><path d="m6 2 3 3-3 3"/>`)
	iconRecord       = lucide(`<circle cx="12" cy="12" r="7"/>`)
	iconVolumeOn     = lucide(`<polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/><path d="M15.54 8.46a5 5 0 0 1 0 7.07"/><path d="M19.07 4.93a10 10 0 0 1 0 14.14"/>`)
	iconVolumeOff    = lucide(`<polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/><line x1="22" x2="16" y1="9" y2="15"/><line x1="16" x2="22" y1="9" y2="15"/>`)
	iconClipboard    = lucide(`<rect width="8" height="4" x="8" y="2" rx="1" ry="1"/><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"/>`)
)

// Icons of the V2.1 shell and panes (Lucide, ISC license).
var (
	iconPlus       = lucide(`<path d="M5 12h14"/><path d="M12 5v14"/>`)
	iconLink       = lucide(`<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>`)
	iconChevron    = lucide(`<path d="m9 18 6-6-6-6"/>`)
	iconMore       = lucide(`<circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/><circle cx="5" cy="12" r="1"/>`)
	iconPopOut     = lucide(`<path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>`)
	iconDock       = lucide(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M9 3v18"/><path d="m16 15-3-3 3-3"/>`)
	iconMaximize   = lucide(`<rect width="18" height="18" x="3" y="3" rx="2"/>`)
	iconSplitRight = lucide(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M12 3v18"/>`)
	iconSplitDown  = lucide(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M3 12h18"/>`)
	iconGlobe      = lucide(`<circle cx="12" cy="12" r="10"/><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"/><path d="M2 12h20"/>`)
)

// Icons of the V3 shell (Lucide, ISC license).
var (
	iconSidebar    = lucide(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M9 3v18"/>`)
	iconSearch     = lucide(`<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>`)
	iconSun        = lucide(`<circle cx="12" cy="12" r="4"/><path d="M12 2v2"/><path d="M12 20v2"/><path d="m4.93 4.93 1.41 1.41"/><path d="m17.66 17.66 1.41 1.41"/><path d="M2 12h2"/><path d="M20 12h2"/><path d="m6.34 17.66-1.41 1.41"/><path d="m19.07 4.93-1.41 1.41"/>`)
	iconMoon       = lucide(`<path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/>`)
	iconAuto       = lucide(`<rect width="20" height="14" x="2" y="3" rx="2"/><path d="M12 3v14"/><path d="M8 21h8"/>`)
	iconMesh       = lucide(`<circle cx="5" cy="6" r="2"/><circle cx="19" cy="6" r="2"/><circle cx="12" cy="19" r="2"/><path d="M7 6h10"/><path d="m6 8 5 9"/><path d="m18 8-5 9"/>`)
	iconSend       = lucide(`<path d="M14.536 21.686a.5.5 0 0 0 .937-.024l6.5-19a.496.496 0 0 0-.635-.635l-19 6.5a.5.5 0 0 0-.024.937l7.93 3.18a2 2 0 0 1 1.112 1.11z"/><path d="m21.854 2.147-10.94 10.939"/>`)
	iconMirror     = lucide(`<rect width="14" height="20" x="5" y="2" rx="2"/><path d="M9 7h6v8H9z"/>`)
	iconAppWin     = lucide(`<rect width="18" height="16" x="3" y="4" rx="2"/><path d="M3 9h18"/>`)
	iconVM         = lucide(`<rect width="14" height="10" x="2" y="4" rx="1.5"/><rect width="7" height="11" x="15" y="9" rx="1.5"/><path d="M6 18h6"/><path d="M9 14v4"/>`)
	iconKey        = lucide(`<path d="m15.5 7.5 2.3 2.3a1 1 0 0 0 1.4 0l2.1-2.1a1 1 0 0 0 0-1.4L19 4"/><path d="m21 2-9.6 9.6"/><circle cx="7.5" cy="15.5" r="5.5"/>`)
	iconSpark      = lucide(`<path d="M9.937 15.5A2 2 0 0 0 8.5 14.063l-6.135-1.582a.5.5 0 0 1 0-.962L8.5 9.936A2 2 0 0 0 9.937 8.5l1.582-6.135a.5.5 0 0 1 .963 0L14.063 8.5A2 2 0 0 0 15.5 9.937l6.135 1.581a.5.5 0 0 1 0 .964L15.5 14.063a2 2 0 0 0-1.437 1.437l-1.582 6.135a.5.5 0 0 1-.963 0z"/>`)
	iconStop       = lucide(`<rect width="14" height="14" x="5" y="5" rx="2"/>`)
	iconFolder     = lucide(`<path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>`)
	iconLog        = lucide(`<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M10 13H8"/><path d="M16 17H8"/><path d="M16 13h-2"/>`)
	iconDownload   = lucide(`<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="m7 10 5 5 5-5"/><path d="M12 15V3"/>`)
	iconTrash      = lucide(`<path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/>`)
	iconEdit       = lucide(`<path d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z"/>`)
	iconMic        = lucide(`<path d="M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3Z"/><path d="M19 10v2a7 7 0 0 1-14 0v-2"/><path d="M12 19v3"/>`)
	iconLock       = lucide(`<rect width="18" height="11" x="3" y="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>`)
	iconCheck      = lucide(`<path d="M20 6 9 17l-5-5"/>`)
	iconChevDown   = lucide(`<path d="m6 9 6 6 6-6"/>`)
	iconWarn       = lucide(`<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"/><path d="M12 9v4"/><path d="M12 17h.01"/>`)
	iconCopy       = lucide(`<rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>`)
	iconPower      = lucide(`<path d="M12 2v10"/><path d="M18.4 6.6a9 9 0 1 1-12.77.04"/>`)
	iconBell       = lucide(`<path d="M10.268 21a2 2 0 0 0 3.464 0"/><path d="M3.262 15.326A1 1 0 0 0 4 17h16a1 1 0 0 0 .74-1.673C19.41 13.956 18 12.499 18 8A6 6 0 0 0 6 8c0 4.499-1.411 5.956-2.738 7.326"/>`)
	iconBattery    = lucide(`<rect width="16" height="10" x="2" y="7" rx="2" ry="2"/><line x1="22" x2="22" y1="11" y2="13"/><line x1="6" x2="6" y1="11" y2="13"/><line x1="10" x2="10" y1="11" y2="13"/>`)
	iconCPU        = lucide(`<rect width="16" height="16" x="4" y="4" rx="2"/><rect width="6" height="6" x="9" y="9" rx="1"/><path d="M15 2v2"/><path d="M15 20v2"/><path d="M2 15h2"/><path d="M2 9h2"/><path d="M20 15h2"/><path d="M20 9h2"/><path d="M9 2v2"/><path d="M9 20v2"/>`)
	iconPulse      = iconDiagnostics
	iconUpdate     = lucide(`<path d="M12 3v12"/><path d="m8 11 4 4 4-4"/><path d="M8 5H4a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-4"/>`)
	iconTray       = lucide(`<path d="M22 12h-6l-2 3h-4l-2-3H2"/><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/>`)
	iconHand       = lucide(`<path d="M18 11V6a2 2 0 0 0-2-2a2 2 0 0 0-2 2"/><path d="M14 10V4a2 2 0 0 0-2-2a2 2 0 0 0-2 2v2"/><path d="M10 10.5V6a2 2 0 0 0-2-2a2 2 0 0 0-2 2v8"/><path d="M18 8a2 2 0 1 1 4 0v6a8 8 0 0 1-8 8h-2c-2.8 0-4.5-.86-5.99-2.34l-3.6-3.6a2 2 0 0 1 2.83-2.82L7 15"/>`)
	iconEye        = lucide(`<path d="M2.062 12.348a1 1 0 0 1 0-.696 10.75 10.75 0 0 1 19.876 0 1 1 0 0 1 0 .696 10.75 10.75 0 0 1-19.876 0"/><circle cx="12" cy="12" r="3"/>`)
	iconFile       = lucide(`<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/>`)
	iconScreenshot = lucide(`<path d="M3 7V5a2 2 0 0 1 2-2h2"/><path d="M17 3h2a2 2 0 0 1 2 2v2"/><path d="M21 17v2a2 2 0 0 1-2 2h-2"/><path d="M7 21H5a2 2 0 0 1-2-2v-2"/><circle cx="12" cy="12" r="3"/>`)
)

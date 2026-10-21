package stealth

import (
	"encoding/json"
	"strings"
)

// stealthJS patches the usual headless/automation tells. It runs before any
// page script on every new document.
func stealthJS(locale string) string {
	lang, _ := json.Marshal([]string{locale, strings.SplitN(locale, "-", 2)[0]})
	return `(() => {
  const def = (obj, prop, getter) => {
    try { Object.defineProperty(obj, prop, { get: getter, configurable: true }); } catch (e) {}
  };

  // navigator.webdriver must be false/absent like a normal browser.
  def(Navigator.prototype, 'webdriver', () => false);
  delete Navigator.prototype.webdriver;

  def(Navigator.prototype, 'languages', () => ` + string(lang) + `);
  def(Navigator.prototype, 'hardwareConcurrency', () => 8);
  def(Navigator.prototype, 'deviceMemory', () => 8);

  // Headless has an empty plugin list.
  if (!navigator.plugins || navigator.plugins.length === 0) {
    const mk = (name, filename, desc) => ({ name, filename, description: desc, length: 1 });
    const plugins = [
      mk('PDF Viewer', 'internal-pdf-viewer', 'Portable Document Format'),
      mk('Chrome PDF Viewer', 'internal-pdf-viewer', 'Portable Document Format'),
      mk('Chromium PDF Viewer', 'internal-pdf-viewer', 'Portable Document Format'),
    ];
    def(Navigator.prototype, 'plugins', () => plugins);
    def(Navigator.prototype, 'mimeTypes', () => [{ type: 'application/pdf' }, { type: 'text/pdf' }]);
  }

  // window.chrome exists in real Chrome.
  if (!window.chrome) { window.chrome = {}; }
  if (!window.chrome.runtime) { window.chrome.runtime = {}; }
  if (!window.chrome.app) {
    window.chrome.app = { isInstalled: false, InstallState: {}, RunningState: {} };
  }
  window.chrome.loadTimes = window.chrome.loadTimes || function () { return {}; };
  window.chrome.csi = window.chrome.csi || function () { return {}; };

  // Notification permission query leaks headless ("denied" while prompt).
  const origQuery = window.navigator.permissions && window.navigator.permissions.query;
  if (origQuery) {
    window.navigator.permissions.query = (p) =>
      p && p.name === 'notifications'
        ? Promise.resolve({ state: Notification.permission === 'denied' ? 'prompt' : Notification.permission, onchange: null })
        : origQuery.call(window.navigator.permissions, p);
  }

  // WebGL vendor/renderer: "Google SwiftShader" gives headless away.
  const patchGL = (proto) => {
    const orig = proto.getParameter;
    proto.getParameter = function (p) {
      if (p === 37445) return 'Google Inc. (Apple)';
      if (p === 37446) return 'ANGLE (Apple, ANGLE Metal Renderer: Apple M1, Unspecified Version)';
      return orig.call(this, p);
    };
  };
  if (window.WebGLRenderingContext) patchGL(WebGLRenderingContext.prototype);
  if (window.WebGL2RenderingContext) patchGL(WebGL2RenderingContext.prototype);

  // Zero outer window size is a headless tell.
  if (window.outerWidth === 0) def(window, 'outerWidth', () => window.innerWidth);
  if (window.outerHeight === 0) def(window, 'outerHeight', () => window.innerHeight + 85);

  // Input dispatched over DevTools reports screenX/Y equal to clientX/Y,
  // which real browsers never do (the window sits somewhere on screen).
  // Offset them by a stable per-document window position.
  const winX = 40 + Math.floor(Math.random() * 260);
  const winY = 30 + Math.floor(Math.random() * 120);
  const chromeH = 85; // tab strip + toolbar
  const nativeSX = Object.getOwnPropertyDescriptor(MouseEvent.prototype, 'screenX');
  const nativeSY = Object.getOwnPropertyDescriptor(MouseEvent.prototype, 'screenY');
  if (nativeSX && nativeSX.get && nativeSY && nativeSY.get) {
    def(MouseEvent.prototype, 'screenX', function () { return nativeSX.get.call(this) + winX; });
    def(MouseEvent.prototype, 'screenY', function () { return nativeSY.get.call(this) + winY + chromeH; });
  }
  def(window, 'screenX', () => winX);
  def(window, 'screenY', () => winY);
  def(window, 'screenLeft', () => winX);
  def(window, 'screenTop', () => winY);

  // Closed shadow roots hide widgets (e.g. Turnstile) from automation;
  // open them so they can be located.
  const origAttach = Element.prototype.attachShadow;
  const attachPatched = function attachShadow(init) {
    return origAttach.call(this, Object.assign({}, init, { mode: 'open' }));
  };
  Element.prototype.attachShadow = attachPatched;

  // Hide function patching: toString should look native.
  const nativeToString = Function.prototype.toString;
  const patched = new WeakSet([window.navigator.permissions && window.navigator.permissions.query, attachPatched]);
  Function.prototype.toString = function () {
    if (patched.has(this)) return 'function ' + (this.name || '') + '() { [native code] }';
    return nativeToString.call(this);
  };
})();`
}

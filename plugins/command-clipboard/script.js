const fs = require("fs");
const path = require("path");
const server = require("server");

const browserScript = fs.readFileSync(path.join(__dirname, "browser.js"), "utf8");
const stylesheet = fs.readFileSync(path.join(__dirname, "style.css"), "utf8");

const safeScript = browserScript.replace(/<\/script/gi, "<\\/script");
const safeStyle = stylesheet.replace(/<\/style/gi, "<\\/style");

server.injectHTML(
  `<style id="komari-command-clipboard-style">${safeStyle}</style><script>${safeScript}</script>`,
  '<div id="komari-command-clipboard-root"></div>',
);

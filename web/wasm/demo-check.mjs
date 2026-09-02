// Drives demo.html in headless Chromium and prints the console and window.__demo. Exit 1 on error.
//
//   go run ./hack/tunneldemo            # prints the address; serves web/ on 127.0.0.1:19080
//   PLAYWRIGHT_PATH=/path/to/node_modules/playwright/index.mjs \
//     node web/wasm/demo-check.mjs "http://127.0.0.1:19080/wasm/demo.html?addr=tc…&auto=1"
//
// PLAYWRIGHT_PATH is only needed when "playwright" is not resolvable from this file.
const { chromium } = await import(process.env.PLAYWRIGHT_PATH ?? "playwright");
const url = process.argv[2];
if (!url) { console.error("usage: node demo-check.mjs <demo.html URL with ?addr=…&auto=1>"); process.exit(2); }
const browser = await chromium.launch();
const page = await browser.newPage();
page.on("console", (m) => console.log("[console]", m.text()));
page.on("pageerror", (e) => console.log("[pageerror]", e.message));
await page.goto(url);
await page.waitForFunction(() => window.__demo?.done || window.__demo?.error, null, { timeout: 150_000 });
const demo = await page.evaluate(() => window.__demo);
console.log("[__demo]", JSON.stringify(demo, null, 2));
await browser.close();
process.exit(demo.error ? 1 : 0);

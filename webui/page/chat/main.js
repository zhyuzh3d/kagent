import { runAuthGuard } from "./lib/auth-guard.js";

(async () => {
  await runAuthGuard();
  const { initChatPage } = await import("./components/chat-page.js");
  await initChatPage();
})().catch((error) => {
  console.error("[chat] bootstrap failed", error);
});

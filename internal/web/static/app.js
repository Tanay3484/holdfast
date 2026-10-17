// Holdfast: live refresh, async forms, copy buttons. The site works without this script; it just
// stops refreshing on its own.
(() => {
  "use strict";

  const toastEl = () => document.querySelector(".toast");
  let toastTimer;
  function toast(msg, bad) {
    const el = toastEl();
    if (!el) return;
    el.textContent = msg;
    el.classList.toggle("toast--bad", !!bad);
    el.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { el.hidden = true; }, 3200);
  }

  // Elements with data-poll="/url" are replaced with fresh HTML every two seconds while the tab is visible.
  const pollers = new Map();
  async function refresh(el) {
    const url = el.dataset.poll;
    if (!url) return;
    try {
      const res = await fetch(url, { headers: { Accept: "text/html" }, cache: "no-store" });
      if (!res.ok) return;
      const html = await res.text();
      if (html === el.dataset.last) return;
      const tpl = document.createElement("template");
      tpl.innerHTML = html.trim();
      // An event fragment carries its own outer node; a board fragment is bare children.
      const inner = tpl.content.querySelector("#" + el.id) || tpl.content;
      const firstLoad = !el.dataset.last;
      el.replaceChildren(...inner.childNodes);
      el.dataset.last = html;
      if (!firstLoad) { el.classList.remove("fresh"); void el.offsetWidth; el.classList.add("fresh"); }
      if (el.querySelector("[data-final]")) { clearInterval(pollers.get(el)); }
    } catch (_) { /* offline for a moment; try again next tick */ }
  }
  function startPolling() {
    document.querySelectorAll("[data-poll]").forEach((el) => {
      if (!el.dataset.poll || pollers.has(el)) return;
      el.dataset.last = "";
      pollers.set(el, setInterval(() => { if (!document.hidden) refresh(el); }, 2000));
    });
  }
  function refreshAll() { pollers.forEach((_, el) => refresh(el)); }

  // Forms with data-async post in the background and show the answer as a toast.
  document.addEventListener("submit", async (e) => {
    const form = e.target;
    if (!form.matches("form[data-async]")) return;
    e.preventDefault();
    if (form.dataset.confirm && !window.confirm(form.dataset.confirm)) return;
    const btn = form.querySelector("button");
    if (btn) btn.disabled = true;
    try {
      const res = await fetch(form.action, { method: "POST", headers: { Accept: "application/json" }, body: new FormData(form) });
      const data = await res.json().catch(() => ({}));
      toast(data.message || (res.ok ? "Done." : "That didn't work."), !res.ok);
      if (res.ok && "follow" in form.dataset && data.redirect) { setTimeout(() => { location.href = data.redirect; }, 900); return; }
      if (res.ok && form.querySelector("input[name=secret]")) { setTimeout(() => location.reload(), 700); }
      setTimeout(refreshAll, 300);
      setTimeout(refreshAll, 1200);
    } catch (_) {
      toast("Couldn't reach Holdfast. Check your connection.", true);
    } finally {
      if (btn) setTimeout(() => { btn.disabled = false; }, 1200);
    }
  });

  // Copy buttons: data-copy="#selector" copies that element's text, data-copy-text copies the value.
  document.addEventListener("click", async (e) => {
    const btn = e.target.closest("[data-copy],[data-copy-text]");
    if (!btn) return;
    const text = btn.dataset.copyText ?? document.querySelector(btn.dataset.copy)?.textContent?.trim() ?? "";
    try {
      await navigator.clipboard.writeText(text);
      toast("Copied.");
    } catch (_) {
      toast("Couldn't copy. Select the text instead.", true);
    }
  });

  document.addEventListener("DOMContentLoaded", startPolling);
})();

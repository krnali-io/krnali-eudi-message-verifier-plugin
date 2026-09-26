"use strict";
const button = document.querySelector("#verify");
const status = document.querySelector("#verification-status");
let started = false, pollKey = "", timer;
const terminal = {
  verified: "Done. You can go back to your chat.",
  mismatch: "Done. You can go back to your chat.",
  declined: "You declined to share. You can go back to your chat.",
  failed: "The check could not be completed. Ask for a new link.",
  expired: "The check expired. Ask for a new link.",
  cancelled: "This request was cancelled. Ask for a new link."
};

function countdown() {
  if (started) return;
  const seconds = Math.max(0, Math.ceil((new Date(document.body.dataset.expires) - new Date()) / 1000));
  document.querySelector("#countdown").textContent = Math.floor(seconds / 60) + ":" + String(seconds % 60).padStart(2, "0") + " left";
  if (!seconds) {
    button.disabled = true;
    status.textContent = "This link has expired. Ask for a new link.";
  }
}
countdown();
setInterval(countdown, 1000);

async function poll() {
  if (!pollKey) return;
  try {
    const r = await fetch("/s/" + encodeURIComponent(pollKey), {cache: "no-store"});
    if (!r.ok) throw Error();
    const data = await r.json();
    if (terminal[data.status]) {
      status.textContent = terminal[data.status];
      document.querySelector("#wallet-actions").hidden = true;
      document.querySelector("#qr").replaceChildren();
      document.querySelector("#open-wallet").removeAttribute("href");
      pollKey = "";
      return;
    }
    status.textContent = "Waiting for wallet approval.";
  } catch {
    status.textContent = "Reconnecting to your verification…";
  }
  timer = setTimeout(poll, 2000);
}

button.addEventListener("click", async () => {
  if (started) return;
  button.disabled = true;
  status.textContent = "Preparing your wallet request…";
  try {
    const r = await fetch(document.body.dataset.start, {method: "POST", cache: "no-store"});
    if (!r.ok) {
      if (r.status === 410) {
        started = true;
        throw Error("This link is no longer available. Ask for a new link.");
      }
      throw Error("The check could not start. Please try again or ask for a new link.");
    }
    const data = await r.json();
    const u = new URL(data.authorization_request_uri);
    if (!["haip-vp:", "openid4vp:"].includes(u.protocol)) throw Error("Wallet link unavailable. Ask for a new link.");
    started = true;
    pollKey = data.poll_key;
    button.hidden = true;
    document.querySelector("#wallet-actions").hidden = false;
    // A separate tap preserves a user gesture for the native app handoff.
    // Do not rewrite the scheme returned by the verifier for a particular wallet.
    document.querySelector("#open-wallet").href = data.authorization_request_uri;
    const qr = document.querySelector("#qr");
    const qrHelp = document.querySelector("#qr-help");
    qrHelp.hidden = false;
    try {
      if (!window.QRCode) throw Error();
      new QRCode(qr, {text: data.authorization_request_uri, width: 240, height: 240, correctLevel: QRCode.CorrectLevel.M});
    } catch {
      qr.replaceChildren();
      qrHelp.textContent = "The QR code could not load. You can still use Open wallet on this phone. For a new QR attempt, ask for a fresh link.";
    }
    poll();
  } catch (e) {
    status.textContent = e.message;
    if (!started) button.disabled = false;
  }
});
window.addEventListener("pagehide", () => clearTimeout(timer));
window.addEventListener("pageshow", () => {
  if (pollKey) {
    clearTimeout(timer);
    poll();
  }
});

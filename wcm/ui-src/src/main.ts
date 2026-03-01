import * as api from "./api";
import type { Credential, Session, ApprovalEvent } from "./api";
import { SSEClient } from "./sse";

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

type Section = "credentials" | "sessions" | "approvals";

const state = {
  userId: "",
  credentials: [] as Credential[],
  sessions: [] as Session[],
  pendingApprovals: new Map<string, ApprovalEvent>(),
  section: "credentials" as Section,
  sseConnected: false,
  countdownHandle: 0,
};

// ---------------------------------------------------------------------------
// Bootstrap
// ---------------------------------------------------------------------------

document.addEventListener("DOMContentLoaded", () => {
  const stored = localStorage.getItem("wcm_user_id");
  if (stored) {
    state.userId = stored;
    startApp();
  } else {
    renderSetup();
  }
});

function renderSetup(): void {
  document.body.innerHTML = `
    <div class="setup-card">
      <h1>Credential Manager</h1>
      <p>Enter your user email to continue.</p>
      <input id="setup-email" type="email" placeholder="you@example.com" autofocus />
      <button id="setup-btn">Continue</button>
    </div>`;

  const btn = document.getElementById("setup-btn")!;
  const input = document.getElementById("setup-email") as HTMLInputElement;

  const submit = () => {
    const val = input.value.trim();
    if (!val) return;
    localStorage.setItem("wcm_user_id", val);
    state.userId = val;
    startApp();
  };

  btn.addEventListener("click", submit);
  input.addEventListener("keydown", e => { if (e.key === "Enter") submit(); });
}

function startApp(): void {
  renderShell();
  navigate(state.section);
  startSSE();
}

// ---------------------------------------------------------------------------
// Shell (top bar + sidebar)
// ---------------------------------------------------------------------------

function renderShell(): void {
  document.body.innerHTML = `
    <header id="topbar">
      <span class="brand">🔑 Credential Manager</span>
      <span class="user-info">
        <span id="sse-dot" class="dot dot-grey" title="Connecting…"></span>
        <span id="user-label">${esc(state.userId)}</span>
        <button id="logout-btn" class="btn-ghost">Change user</button>
      </span>
    </header>
    <div class="layout">
      <nav id="sidebar">
        <a class="nav-item" data-section="credentials">Credentials</a>
        <a class="nav-item" data-section="sessions">Sessions</a>
        <a class="nav-item" data-section="approvals">
          Approvals <span id="approval-badge" class="badge hidden">0</span>
        </a>
      </nav>
      <main id="main"></main>
    </div>
    <div id="modal-overlay" class="hidden">
      <div id="modal-box"></div>
    </div>`;

  document.getElementById("logout-btn")!.addEventListener("click", () => {
    localStorage.removeItem("wcm_user_id");
    location.reload();
  });

  document.getElementById("sidebar")!.addEventListener("click", e => {
    const target = (e.target as HTMLElement).closest("[data-section]") as HTMLElement | null;
    if (target?.dataset["section"]) navigate(target.dataset["section"] as Section);
  });
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

function navigate(section: Section): void {
  state.section = section;

  document.querySelectorAll(".nav-item").forEach(el => {
    el.classList.toggle("active", (el as HTMLElement).dataset["section"] === section);
  });

  if (section === "credentials") loadCredentials();
  else if (section === "sessions") loadSessions();
  else renderApprovals();
}

// ---------------------------------------------------------------------------
// Credentials section
// ---------------------------------------------------------------------------

async function loadCredentials(): Promise<void> {
  setMain(`<div class="loading">Loading…</div>`);
  try {
    state.credentials = await api.credentials.list(state.userId);
    renderCredentials();
  } catch {
    setMain(`<div class="error">Failed to load credentials.</div>`);
  }
}

function renderCredentials(): void {
  const rows = state.credentials.length
    ? state.credentials.map(c => `
        <tr>
          <td>${esc(c.label)}</td>
          <td>${esc(c.attributes["service"] ?? "—")}</td>
          <td>${esc(c.attributes["account"] ?? "—")}</td>
          <td>
            <span class="secret-mask" data-id="${c.id}">••••••••</span>
            <button class="btn-icon" data-reveal="${c.id}" title="Toggle secret">👁</button>
          </td>
          <td class="actions">
            <button class="btn-sm" data-edit="${c.id}">Edit</button>
            <button class="btn-sm btn-danger" data-delete="${c.id}">Delete</button>
          </td>
        </tr>`).join("")
    : `<tr><td colspan="5" class="empty">No credentials stored yet.</td></tr>`;

  setMain(`
    <div class="section-header">
      <h2>Credentials</h2>
      <button id="new-cred-btn" class="btn-primary">+ New</button>
    </div>
    <table>
      <thead><tr><th>Label</th><th>Service</th><th>Account</th><th>Secret</th><th>Actions</th></tr></thead>
      <tbody>${rows}</tbody>
    </table>`);

  document.getElementById("new-cred-btn")!.addEventListener("click", () => openCredentialModal());

  document.getElementById("main")!.addEventListener("click", e => {
    const el = e.target as HTMLElement;
    const editId = el.dataset["edit"];
    const deleteId = el.dataset["delete"];
    const revealId = el.dataset["reveal"];

    if (editId) {
      const cred = state.credentials.find(c => c.id === editId);
      if (cred) openCredentialModal(cred);
    }
    if (deleteId) confirmDelete(deleteId);
    if (revealId) toggleSecret(revealId, el);
  });
}

function toggleSecret(id: string, btn: HTMLElement): void {
  const span = document.querySelector<HTMLSpanElement>(`.secret-mask[data-id="${id}"]`);
  if (!span) return;
  const cred = state.credentials.find(c => c.id === id);
  if (!cred) return;
  if (span.textContent === "••••••••") {
    span.textContent = cred.secret;
    btn.title = "Hide secret";
  } else {
    span.textContent = "••••••••";
    btn.title = "Toggle secret";
  }
}

function openCredentialModal(cred?: Credential): void {
  const isEdit = !!cred;
  const attrs = cred?.attributes ?? {};
  const attrRows = Object.entries(attrs).map(([k, v]) => attrRow(k, v)).join("");

  showModal(`
    <h2>${isEdit ? "Edit" : "New"} Credential</h2>
    <form id="cred-form">
      <label>Label <input name="label" value="${esc(cred?.label ?? "")}" required /></label>
      <label>Secret <input name="secret" type="text" value="${esc(cred?.secret ?? "")}" required /></label>
      <label>Collection <input name="collection" value="${esc(cred?.collection ?? "default")}" /></label>
      <fieldset>
        <legend>Attributes</legend>
        <div id="attr-list">${attrRows}</div>
        <button type="button" id="add-attr-btn" class="btn-ghost">+ Add attribute</button>
      </fieldset>
      <div class="modal-actions">
        <button type="button" id="modal-cancel" class="btn-ghost">Cancel</button>
        <button type="submit" class="btn-primary">${isEdit ? "Save" : "Create"}</button>
      </div>
    </form>`);

  document.getElementById("add-attr-btn")!.addEventListener("click", () => {
    document.getElementById("attr-list")!.insertAdjacentHTML("beforeend", attrRow("", ""));
  });

  document.getElementById("modal-cancel")!.addEventListener("click", closeModal);

  document.getElementById("cred-form")!.addEventListener("submit", async e => {
    e.preventDefault();
    const form = e.target as HTMLFormElement;
    const data = new FormData(form);
    const attributes: Record<string, string> = {};
    document.querySelectorAll<HTMLInputElement>(".attr-key").forEach((keyInput, i) => {
      const valInput = document.querySelectorAll<HTMLInputElement>(".attr-val")[i];
      if (keyInput.value.trim()) attributes[keyInput.value.trim()] = valInput?.value ?? "";
    });

    const body = {
      label: data.get("label") as string,
      secret: data.get("secret") as string,
      collection: data.get("collection") as string,
      attributes,
    };

    try {
      if (isEdit && cred) {
        const updated = await api.credentials.update(state.userId, cred.id, body);
        state.credentials = state.credentials.map(c => c.id === cred.id ? updated : c);
      } else {
        const created = await api.credentials.create(state.userId, body);
        state.credentials.push(created);
      }
      closeModal();
      renderCredentials();
    } catch (err) {
      alert(`Error: ${(err as Error).message}`);
    }
  });
}

function attrRow(key: string, value: string): string {
  return `<div class="attr-row">
    <input class="attr-key" placeholder="key"  value="${esc(key)}" />
    <input class="attr-val" placeholder="value" value="${esc(value)}" />
    <button type="button" class="btn-icon attr-remove" title="Remove">✕</button>
  </div>`;
}

document.addEventListener("click", e => {
  if ((e.target as HTMLElement).classList.contains("attr-remove")) {
    (e.target as HTMLElement).closest(".attr-row")?.remove();
  }
});

async function confirmDelete(id: string): Promise<void> {
  const cred = state.credentials.find(c => c.id === id);
  if (!cred) return;
  if (!confirm(`Delete "${cred.label}"?`)) return;
  try {
    await api.credentials.remove(state.userId, id);
    state.credentials = state.credentials.filter(c => c.id !== id);
    renderCredentials();
  } catch {
    alert("Failed to delete credential.");
  }
}

// ---------------------------------------------------------------------------
// Sessions section
// ---------------------------------------------------------------------------

async function loadSessions(): Promise<void> {
  setMain(`<div class="loading">Loading…</div>`);
  try {
    state.sessions = await api.sessions.list(state.userId);
    renderSessions();
  } catch {
    setMain(`<div class="error">Failed to load sessions.</div>`);
  }
}

function renderSessions(): void {
  clearInterval(state.countdownHandle);

  const rows = state.sessions.length
    ? state.sessions.map(s => {
        const statusClass = s.locked ? "badge-locked" : "badge-unlocked";
        const statusText  = s.locked ? "🔒 locked" : "🔓 unlocked";
        const expiry = !s.locked && s.expiresAt
          ? `<span class="countdown" data-expires="${s.expiresAt}"></span>`
          : "";
        const action = s.locked
          ? `<button class="btn-sm btn-primary" data-unlock="${s.sessionId}">Unlock</button>`
          : `<button class="btn-sm btn-danger"  data-lock="${s.sessionId}">Lock</button>`;
        return `<tr>
          <td class="mono">${esc(s.sessionId.slice(0, 8))}…</td>
          <td>${esc(s.workspaceId || "—")}</td>
          <td><span class="badge ${statusClass}">${statusText}</span> ${expiry}</td>
          <td class="actions">${action}</td>
        </tr>`;
      }).join("")
    : `<tr><td colspan="4" class="empty">No active D-Bus sessions.</td></tr>`;

  setMain(`
    <div class="section-header"><h2>D-Bus Sessions</h2></div>
    <table>
      <thead><tr><th>Session ID</th><th>Workspace</th><th>Status</th><th>Actions</th></tr></thead>
      <tbody>${rows}</tbody>
    </table>`);

  // Countdown timers for unlocked sessions.
  state.countdownHandle = window.setInterval(tickCountdowns, 1_000);
  tickCountdowns();

  document.getElementById("main")!.addEventListener("click", async e => {
    const el = e.target as HTMLElement;
    const unlockId = el.dataset["unlock"];
    const lockId   = el.dataset["lock"];

    if (unlockId) {
      el.disabled = true;
      await directUnlock(unlockId);
      await loadSessions();
    }
    if (lockId) {
      el.disabled = true;
      await api.sessions.lock(state.userId, lockId).catch(() => null);
      await loadSessions();
    }
  });
}

// Unlock from UI: request unlock then immediately approve — no approval page needed.
async function directUnlock(sessionId: string): Promise<void> {
  try {
    const res = await api.sessions.unlock(state.userId, sessionId);
    const approvalId = res.approvalId;
    await api.approvals.approve(approvalId);
  } catch {
    alert("Failed to unlock session.");
  }
}

function tickCountdowns(): void {
  document.querySelectorAll<HTMLElement>(".countdown").forEach(el => {
    const expires = new Date(el.dataset["expires"]!).getTime();
    const remaining = Math.max(0, Math.floor((expires - Date.now()) / 1_000));
    const m = Math.floor(remaining / 60);
    const s = remaining % 60;
    el.textContent = remaining > 0
      ? `(expires in ${m}:${String(s).padStart(2, "0")})`
      : "(expired)";
  });
}

// ---------------------------------------------------------------------------
// Approvals section
// ---------------------------------------------------------------------------

function renderApprovals(): void {
  const pending = [...state.pendingApprovals.values()];

  const rows = pending.length
    ? pending.map(req => `
        <tr id="approval-row-${req.id}">
          <td>${esc(req.applicationName || "unknown")}</td>
          <td>${esc(req.workspaceId || "—")}</td>
          <td>${new Date(req.requestedAt).toLocaleTimeString()}</td>
          <td><span class="countdown" data-expires="${req.expiresAt}"></span></td>
          <td class="actions">
            <button class="btn-sm btn-primary" data-approve="${req.id}">✓ Approve</button>
            <button class="btn-sm btn-danger"  data-deny="${req.id}">✗ Deny</button>
          </td>
        </tr>`).join("")
    : `<tr><td colspan="5" class="empty">No pending approval requests.</td></tr>`;

  setMain(`
    <div class="section-header"><h2>Pending Approvals</h2></div>
    <table>
      <thead><tr><th>Application</th><th>Workspace</th><th>Requested</th><th>Expires</th><th>Actions</th></tr></thead>
      <tbody>${rows}</tbody>
    </table>`);

  state.countdownHandle = window.setInterval(tickCountdowns, 1_000);
  tickCountdowns();

  document.getElementById("main")!.addEventListener("click", async e => {
    const el = e.target as HTMLElement;
    const approveId = el.dataset["approve"];
    const denyId    = el.dataset["deny"];

    if (approveId) {
      el.disabled = true;
      await api.approvals.approve(approveId).catch(() => null);
      state.pendingApprovals.delete(approveId);
      renderApprovals();
      updateApprovalBadge();
    }
    if (denyId) {
      el.disabled = true;
      await api.approvals.deny(denyId).catch(() => null);
      state.pendingApprovals.delete(denyId);
      renderApprovals();
      updateApprovalBadge();
    }
  });
}

// ---------------------------------------------------------------------------
// SSE
// ---------------------------------------------------------------------------

function startSSE(): void {
  const url = `/api/users/${encodeURIComponent(state.userId)}/events`;

  new SSEClient(url, connected => {
    state.sseConnected = connected;
    const dot = document.getElementById("sse-dot");
    if (dot) {
      dot.className = `dot ${connected ? "dot-green" : "dot-grey"}`;
      dot.title = connected ? "Connected" : "Reconnecting…";
    }
  })
    .on("approval_requested", data => {
      const req = data as ApprovalEvent;
      state.pendingApprovals.set(req.id, req);
      updateApprovalBadge();
      if (state.section === "approvals") renderApprovals();
    })
    .on("approval_resolved", data => {
      const { approvalId } = data as { approvalId: string };
      state.pendingApprovals.delete(approvalId);
      updateApprovalBadge();
      if (state.section === "approvals") renderApprovals();
    })
    .on("lock_state_changed", data => {
      const evt = data as { sessionId: string; locked: boolean; expiresAt: string | null };
      const s = state.sessions.find(s => s.sessionId === evt.sessionId);
      if (s) {
        s.locked = evt.locked;
        s.expiresAt = evt.expiresAt;
        s.unlockedAt = evt.locked ? null : new Date().toISOString();
        if (state.section === "sessions") renderSessions();
      }
    });
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function setMain(html: string): void {
  clearInterval(state.countdownHandle);
  const main = document.getElementById("main");
  if (main) main.innerHTML = html;
}

function showModal(html: string): void {
  const overlay = document.getElementById("modal-overlay")!;
  const box = document.getElementById("modal-box")!;
  box.innerHTML = html;
  overlay.classList.remove("hidden");
  overlay.addEventListener("click", e => {
    if (e.target === overlay) closeModal();
  }, { once: true });
}

function closeModal(): void {
  document.getElementById("modal-overlay")!.classList.add("hidden");
}

function updateApprovalBadge(): void {
  const badge = document.getElementById("approval-badge");
  if (!badge) return;
  const count = state.pendingApprovals.size;
  badge.textContent = String(count);
  badge.classList.toggle("hidden", count === 0);
}

function esc(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

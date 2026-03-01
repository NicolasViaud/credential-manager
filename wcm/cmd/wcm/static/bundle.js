"use strict";(()=>{var p=e=>`/api/users/${encodeURIComponent(e)}`;async function c(e,t){let n=await fetch(e,t);if(n.status===204)return;let a=await n.json();if(!n.ok)throw Object.assign(new Error(a.message??n.statusText),{status:n.status,body:a});return a}function k(e,t){return{method:e,headers:{"Content-Type":"application/json"},body:JSON.stringify(t)}}var v={list(e){return c(`${p(e)}/credentials`)},create(e,t){return c(`${p(e)}/credentials`,k("POST",t))},update(e,t,n){return c(`${p(e)}/credentials/${t}`,k("PUT",n))},remove(e,t){return c(`${p(e)}/credentials/${t}`,{method:"DELETE"})}},b={list(e){return c(`${p(e)}/sessions`)},unlock(e,t){return fetch(`${p(e)}/sessions/${t}/unlock`,k("POST",{applicationName:"wcm-ui"})).then(n=>n.json())},lock(e,t){return c(`${p(e)}/sessions/${t}/lock`,{method:"POST"})}},f={approve(e){return c(`/api/approvals/${e}/approve`,{method:"POST"})},deny(e){return c(`/api/approvals/${e}/deny`,{method:"POST"})}};var B=["approval_requested","approval_resolved","lock_state_changed"],y=class{constructor(t,n){this.url=t;this.onConnectionChange=n;this.es=null;this.handlers=new Map;this.lastEventId="";this.retryMs=1e3;this.stopped=!1;this.connect()}on(t,n){let a=this.handlers.get(t)??[];return a.push(n),this.handlers.set(t,a),this}close(){this.stopped=!0,this.es?.close()}connect(){if(this.stopped)return;let t=this.lastEventId?`${this.url}?lastEventId=${encodeURIComponent(this.lastEventId)}`:this.url;this.es=new EventSource(t),this.es.onopen=()=>{this.retryMs=1e3,this.onConnectionChange(!0)},this.es.onerror=()=>{this.onConnectionChange(!1),this.es?.close(),setTimeout(()=>this.connect(),this.retryMs),this.retryMs=Math.min(this.retryMs*2,3e4)};for(let n of B)this.es.addEventListener(n,a=>{let o=a;o.lastEventId&&(this.lastEventId=o.lastEventId);try{let i=JSON.parse(o.data);this.handlers.get(n)?.forEach(d=>d(i))}catch{}})}};var s={userId:"",credentials:[],sessions:[],pendingApprovals:new Map,section:"credentials",sseConnected:!1,countdownHandle:0};document.addEventListener("DOMContentLoaded",()=>{let e=localStorage.getItem("wcm_user_id");e?(s.userId=e,x()):_()});function _(){document.body.innerHTML=`
    <div class="setup-card">
      <h1>Credential Manager</h1>
      <p>Enter your user email to continue.</p>
      <input id="setup-email" type="email" placeholder="you@example.com" autofocus />
      <button id="setup-btn">Continue</button>
    </div>`;let e=document.getElementById("setup-btn"),t=document.getElementById("setup-email"),n=()=>{let a=t.value.trim();a&&(localStorage.setItem("wcm_user_id",a),s.userId=a,x())};e.addEventListener("click",n),t.addEventListener("keydown",a=>{a.key==="Enter"&&n()})}function x(){R(),T(s.section),j()}function R(){document.body.innerHTML=`
    <header id="topbar">
      <span class="brand">\u{1F511} Credential Manager</span>
      <span class="user-info">
        <span id="sse-dot" class="dot dot-grey" title="Connecting\u2026"></span>
        <span id="user-label">${r(s.userId)}</span>
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
    </div>`,document.getElementById("logout-btn").addEventListener("click",()=>{localStorage.removeItem("wcm_user_id"),location.reload()}),document.getElementById("sidebar").addEventListener("click",e=>{let t=e.target.closest("[data-section]");t?.dataset.section&&T(t.dataset.section)})}function T(e){s.section=e,document.querySelectorAll(".nav-item").forEach(t=>{t.classList.toggle("active",t.dataset.section===e)}),e==="credentials"?D():e==="sessions"?w():g()}async function D(){u('<div class="loading">Loading\u2026</div>');try{s.credentials=await v.list(s.userId),S()}catch{u('<div class="error">Failed to load credentials.</div>')}}function S(){let e=s.credentials.length?s.credentials.map(t=>`
        <tr>
          <td>${r(t.label)}</td>
          <td>${r(t.attributes.service??"\u2014")}</td>
          <td>${r(t.attributes.account??"\u2014")}</td>
          <td>
            <span class="secret-mask" data-id="${t.id}">\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022</span>
            <button class="btn-icon" data-reveal="${t.id}" title="Toggle secret">\u{1F441}</button>
          </td>
          <td class="actions">
            <button class="btn-sm" data-edit="${t.id}">Edit</button>
            <button class="btn-sm btn-danger" data-delete="${t.id}">Delete</button>
          </td>
        </tr>`).join(""):'<tr><td colspan="5" class="empty">No credentials stored yet.</td></tr>';u(`
    <div class="section-header">
      <h2>Credentials</h2>
      <button id="new-cred-btn" class="btn-primary">+ New</button>
    </div>
    <table>
      <thead><tr><th>Label</th><th>Service</th><th>Account</th><th>Secret</th><th>Actions</th></tr></thead>
      <tbody>${e}</tbody>
    </table>`),document.getElementById("new-cred-btn").addEventListener("click",()=>A()),document.getElementById("main").addEventListener("click",t=>{let n=t.target,a=n.dataset.edit,o=n.dataset.delete,i=n.dataset.reveal;if(a){let d=s.credentials.find(m=>m.id===a);d&&A(d)}o&&U(o),i&&q(i,n)})}function q(e,t){let n=document.querySelector(`.secret-mask[data-id="${e}"]`);if(!n)return;let a=s.credentials.find(o=>o.id===e);a&&(n.textContent==="\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022"?(n.textContent=a.secret,t.title="Hide secret"):(n.textContent="\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022",t.title="Toggle secret"))}function A(e){let t=!!e,n=e?.attributes??{},a=Object.entries(n).map(([o,i])=>C(o,i)).join("");O(`
    <h2>${t?"Edit":"New"} Credential</h2>
    <form id="cred-form">
      <label>Label <input name="label" value="${r(e?.label??"")}" required /></label>
      <label>Secret <input name="secret" type="text" value="${r(e?.secret??"")}" required /></label>
      <label>Collection <input name="collection" value="${r(e?.collection??"default")}" /></label>
      <fieldset>
        <legend>Attributes</legend>
        <div id="attr-list">${a}</div>
        <button type="button" id="add-attr-btn" class="btn-ghost">+ Add attribute</button>
      </fieldset>
      <div class="modal-actions">
        <button type="button" id="modal-cancel" class="btn-ghost">Cancel</button>
        <button type="submit" class="btn-primary">${t?"Save":"Create"}</button>
      </div>
    </form>`),document.getElementById("add-attr-btn").addEventListener("click",()=>{document.getElementById("attr-list").insertAdjacentHTML("beforeend",C("",""))}),document.getElementById("modal-cancel").addEventListener("click",$),document.getElementById("cred-form").addEventListener("submit",async o=>{o.preventDefault();let i=o.target,d=new FormData(i),m={};document.querySelectorAll(".attr-key").forEach((l,h)=>{let H=document.querySelectorAll(".attr-val")[h];l.value.trim()&&(m[l.value.trim()]=H?.value??"")});let L={label:d.get("label"),secret:d.get("secret"),collection:d.get("collection"),attributes:m};try{if(t&&e){let l=await v.update(s.userId,e.id,L);s.credentials=s.credentials.map(h=>h.id===e.id?l:h)}else{let l=await v.create(s.userId,L);s.credentials.push(l)}$(),S()}catch(l){alert(`Error: ${l.message}`)}})}function C(e,t){return`<div class="attr-row">
    <input class="attr-key" placeholder="key"  value="${r(e)}" />
    <input class="attr-val" placeholder="value" value="${r(t)}" />
    <button type="button" class="btn-icon attr-remove" title="Remove">\u2715</button>
  </div>`}document.addEventListener("click",e=>{e.target.classList.contains("attr-remove")&&e.target.closest(".attr-row")?.remove()});async function U(e){let t=s.credentials.find(n=>n.id===e);if(t&&confirm(`Delete "${t.label}"?`))try{await v.remove(s.userId,e),s.credentials=s.credentials.filter(n=>n.id!==e),S()}catch{alert("Failed to delete credential.")}}async function w(){u('<div class="loading">Loading\u2026</div>');try{s.sessions=await b.list(s.userId),M()}catch{u('<div class="error">Failed to load sessions.</div>')}}function M(){clearInterval(s.countdownHandle);let e=s.sessions.length?s.sessions.map(t=>{let n=t.locked?"badge-locked":"badge-unlocked",a=t.locked?"\u{1F512} locked":"\u{1F513} unlocked",o=!t.locked&&t.expiresAt?`<span class="countdown" data-expires="${t.expiresAt}"></span>`:"",i=t.locked?`<button class="btn-sm btn-primary" data-unlock="${t.sessionId}">Unlock</button>`:`<button class="btn-sm btn-danger"  data-lock="${t.sessionId}">Lock</button>`;return`<tr>
          <td class="mono">${r(t.sessionId.slice(0,8))}\u2026</td>
          <td>${r(t.workspaceId||"\u2014")}</td>
          <td><span class="badge ${n}">${a}</span> ${o}</td>
          <td class="actions">${i}</td>
        </tr>`}).join(""):'<tr><td colspan="4" class="empty">No active D-Bus sessions.</td></tr>';u(`
    <div class="section-header"><h2>D-Bus Sessions</h2></div>
    <table>
      <thead><tr><th>Session ID</th><th>Workspace</th><th>Status</th><th>Actions</th></tr></thead>
      <tbody>${e}</tbody>
    </table>`),s.countdownHandle=window.setInterval(E,1e3),E(),document.getElementById("main").addEventListener("click",async t=>{let n=t.target,a=n.dataset.unlock,o=n.dataset.lock;a&&(n.disabled=!0,await N(a),await w()),o&&(n.disabled=!0,await b.lock(s.userId,o).catch(()=>null),await w())})}async function N(e){try{let n=(await b.unlock(s.userId,e)).approvalId;await f.approve(n)}catch{alert("Failed to unlock session.")}}function E(){document.querySelectorAll(".countdown").forEach(e=>{let t=new Date(e.dataset.expires).getTime(),n=Math.max(0,Math.floor((t-Date.now())/1e3)),a=Math.floor(n/60),o=n%60;e.textContent=n>0?`(expires in ${a}:${String(o).padStart(2,"0")})`:"(expired)"})}function g(){let e=[...s.pendingApprovals.values()],t=e.length?e.map(n=>`
        <tr id="approval-row-${n.id}">
          <td>${r(n.applicationName||"unknown")}</td>
          <td>${r(n.workspaceId||"\u2014")}</td>
          <td>${new Date(n.requestedAt).toLocaleTimeString()}</td>
          <td><span class="countdown" data-expires="${n.expiresAt}"></span></td>
          <td class="actions">
            <button class="btn-sm btn-primary" data-approve="${n.id}">\u2713 Approve</button>
            <button class="btn-sm btn-danger"  data-deny="${n.id}">\u2717 Deny</button>
          </td>
        </tr>`).join(""):'<tr><td colspan="5" class="empty">No pending approval requests.</td></tr>';u(`
    <div class="section-header"><h2>Pending Approvals</h2></div>
    <table>
      <thead><tr><th>Application</th><th>Workspace</th><th>Requested</th><th>Expires</th><th>Actions</th></tr></thead>
      <tbody>${t}</tbody>
    </table>`),s.countdownHandle=window.setInterval(E,1e3),E(),document.getElementById("main").addEventListener("click",async n=>{let a=n.target,o=a.dataset.approve,i=a.dataset.deny;o&&(a.disabled=!0,await f.approve(o).catch(()=>null),s.pendingApprovals.delete(o),g(),I()),i&&(a.disabled=!0,await f.deny(i).catch(()=>null),s.pendingApprovals.delete(i),g(),I())})}function j(){let e=`/api/users/${encodeURIComponent(s.userId)}/events`;new y(e,t=>{s.sseConnected=t;let n=document.getElementById("sse-dot");n&&(n.className=`dot ${t?"dot-green":"dot-grey"}`,n.title=t?"Connected":"Reconnecting\u2026")}).on("approval_requested",t=>{let n=t;s.pendingApprovals.set(n.id,n),I(),s.section==="approvals"&&g()}).on("approval_resolved",t=>{let{approvalId:n}=t;s.pendingApprovals.delete(n),I(),s.section==="approvals"&&g()}).on("lock_state_changed",t=>{let n=t,a=s.sessions.find(o=>o.sessionId===n.sessionId);a&&(a.locked=n.locked,a.expiresAt=n.expiresAt,a.unlockedAt=n.locked?null:new Date().toISOString(),s.section==="sessions"&&M())})}function u(e){clearInterval(s.countdownHandle);let t=document.getElementById("main");t&&(t.innerHTML=e)}function O(e){let t=document.getElementById("modal-overlay"),n=document.getElementById("modal-box");n.innerHTML=e,t.classList.remove("hidden"),t.addEventListener("click",a=>{a.target===t&&$()},{once:!0})}function $(){document.getElementById("modal-overlay").classList.add("hidden")}function I(){let e=document.getElementById("approval-badge");if(!e)return;let t=s.pendingApprovals.size;e.textContent=String(t),e.classList.toggle("hidden",t===0)}function r(e){return e.replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;")}})();

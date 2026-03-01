// --- Data models ---

export interface Credential {
  id: string;
  userId: string;
  collection: string;
  label: string;
  secret: string;
  attributes: Record<string, string>;
  createdAt: string;
  updatedAt: string;
}

export interface Session {
  sessionId: string;
  userId: string;
  workspaceId: string;
  locked: boolean;
  unlockedAt: string | null;
  expiresAt: string | null;
}

export interface ApprovalRequest {
  id: string;
  sessionId: string;
  userId: string;
  applicationName: string;
  workspaceId: string;
  status: "pending" | "approved" | "denied" | "expired";
  requestedAt: string;
  expiresAt: string;
}

export interface ApprovalEvent extends ApprovalRequest {
  type: string;
  approvalUrl: string;
}

export interface UnlockResponse {
  approvalId: string;
  approvalUrl: string;
  expiresAt: string;
}

export interface UnlockConflict {
  error: "approval_pending";
  approvalId: string;
  approvalUrl: string;
  expiresAt: string;
}

// --- HTTP helpers ---

const userBase = (userId: string) =>
  `/api/users/${encodeURIComponent(userId)}`;

async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const res = await fetch(url, options);
  if (res.status === 204) return undefined as T;
  const body = await res.json();
  if (!res.ok) throw Object.assign(new Error(body.message ?? res.statusText), { status: res.status, body });
  return body as T;
}

function json(method: string, body: unknown): RequestInit {
  return {
    method,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  };
}

// --- Credentials ---

export const credentials = {
  list(userId: string): Promise<Credential[]> {
    return request(`${userBase(userId)}/credentials`);
  },
  create(userId: string, body: { label: string; secret: string; collection?: string; attributes?: Record<string, string> }): Promise<Credential> {
    return request(`${userBase(userId)}/credentials`, json("POST", body));
  },
  update(userId: string, id: string, body: Partial<Pick<Credential, "label" | "secret" | "collection" | "attributes">>): Promise<Credential> {
    return request(`${userBase(userId)}/credentials/${id}`, json("PUT", body));
  },
  remove(userId: string, id: string): Promise<void> {
    return request(`${userBase(userId)}/credentials/${id}`, { method: "DELETE" });
  },
};

// --- Sessions ---

export const sessions = {
  list(userId: string): Promise<Session[]> {
    return request(`${userBase(userId)}/sessions`);
  },
  // Returns UnlockResponse on 202, or UnlockConflict on 409 (re-use existing pending approval).
  unlock(userId: string, sessionId: string): Promise<UnlockResponse | UnlockConflict> {
    return fetch(`${userBase(userId)}/sessions/${sessionId}/unlock`, json("POST", { applicationName: "wcm-ui" }))
      .then(r => r.json());
  },
  lock(userId: string, sessionId: string): Promise<void> {
    return request(`${userBase(userId)}/sessions/${sessionId}/lock`, { method: "POST" });
  },
};

// --- Approvals ---

export const approvals = {
  approve(approvalId: string): Promise<void> {
    return request(`/api/approvals/${approvalId}/approve`, { method: "POST" });
  },
  deny(approvalId: string): Promise<void> {
    return request(`/api/approvals/${approvalId}/deny`, { method: "POST" });
  },
};

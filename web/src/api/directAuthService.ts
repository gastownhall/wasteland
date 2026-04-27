import type {
  ConnectSessionInput,
  ConnectTokenMetadata,
  RedeemConnectTokenErrorResponse,
  RedeemConnectTokenInput,
  RedeemConnectTokenResponse,
} from "./types";

export class DirectAuthServiceError extends Error {
  constructor(
    message: string,
    public status: number,
    public errorCode?: string,
    public retryable?: boolean,
    public requestId?: string,
  ) {
    super(message);
    this.name = "DirectAuthServiceError";
  }
}

export function buildConnectTokenMetadata(input: ConnectSessionInput): ConnectTokenMetadata {
  return {
    rig_handle: input.rig_handle,
    wastelands: [
      {
        upstream: input.upstream,
        fork_org: input.fork_org,
        fork_db: input.fork_db,
        mode: input.mode ?? "pr",
        signing: input.signing ?? true,
      },
    ],
  };
}

function buildRedeemUrl(baseUrl: string): string {
  return `${baseUrl.replace(/\/+$/, "")}/v1/connect-tokens/redeem`;
}

function authServiceOrigin(baseUrl: string): string {
  try {
    return new URL(baseUrl).origin;
  } catch {
    return baseUrl;
  }
}

export async function redeemConnectToken(input: RedeemConnectTokenInput): Promise<RedeemConnectTokenResponse> {
  let resp: Response;
  try {
    resp = await fetch(buildRedeemUrl(input.auth_service_base_url), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        connect_token: input.connect_token,
        redeem_secret: input.redeem_secret,
        api_key: input.api_key,
        metadata: input.metadata,
      }),
    });
  } catch {
    throw new DirectAuthServiceError(
      `Could not reach the DoltHub auth service at ${authServiceOrigin(input.auth_service_base_url)}. ` +
        "Your browser or network may be blocking that host.",
      0,
      "auth_service_unreachable",
      true,
    );
  }

  let body: RedeemConnectTokenResponse | RedeemConnectTokenErrorResponse | null = null;
  try {
    body = (await resp.json()) as RedeemConnectTokenResponse | RedeemConnectTokenErrorResponse;
  } catch {
    if (!resp.ok) {
      throw new DirectAuthServiceError(resp.statusText || "Auth service request failed", resp.status);
    }
    throw new DirectAuthServiceError("Invalid auth service response", resp.status);
  }

  if (!resp.ok) {
    const errorBody = body as RedeemConnectTokenErrorResponse | null;
    throw new DirectAuthServiceError(
      errorBody?.user_message || resp.statusText || "Auth service request failed",
      resp.status,
      errorBody?.error_code,
      errorBody?.retryable,
      errorBody?.request_id,
    );
  }

  return body as RedeemConnectTokenResponse;
}

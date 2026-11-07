export const base = '/app/loliafrp/api/v1'
export class RequestError extends Error { constructor(message: string, public status: number) { super(message) } }
export async function request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
  const response = await fetch(base + path, {
    method, credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', 'X-LoliaFRP-Request': '1' },
    body: method === 'GET' ? undefined : JSON.stringify(body ?? {}),
  })
  const payload = await response.json().catch(() => null)
  if (!response.ok) throw new RequestError(payload?.error?.message ?? (response.status === 401 ? '飞牛会话或平台授权已失效，请重新登录' : `请求失败（${response.status}）`), response.status)
  return payload.data as T
}
export interface Account { id: number; username: string; email: string; avatar?: string; role: string; max_tunnel_count: number; traffic_limit: number; traffic_used: number; has_qq: boolean; has_kyc: boolean; is_banned: boolean }
export interface Session { authenticated: boolean; user: { uid: string; username: string; isAdmin: boolean }; account?: Account }
export interface Settings { bootAutoStart: boolean; version: string; arch: string }
export interface Process { name: string; state: string; pid: number; restarts: number; error?: string; logs: string[] }
export interface Tunnel { id: number; name: string; type: string; node_id: number; local_ip: string; local_port: number; remote_port: number; custom_domain: string; remark: string; status: string; node_name: string; node_address: string; config: { auto_tls?: boolean; http_redirect?: boolean; protocol?: string; proxy_protocol_version?: string } }
export interface Node { id: number; name: string; status: string; region_code: string; supported_protocols: string[]; need_kyc: boolean; beian_required: boolean; load?: number; bandwidth: number; traffic_ratio: number }
export interface Domain { id: number; domain: string; is_verified: boolean; status: string; has_beian: boolean; verification_subdomain?: string; verification_token?: string; expires_at?: string }
export interface Notice { id: number; title: string; content: string; level: string; action_label?: string; action_url?: string; created_at?: string; updated_at?: string }
export interface Overview { account: Account; stats?: { traffic_limit: number; traffic_used: number; traffic_remaining: number }; summary?: { total_in: number; total_out: number; total_traffic: number }; daily?: { daily_stats: { date: string; total_traffic: number }[] }; announcements?: { items: Notice[] }; warnings: Record<string, string> }
export interface Device { userCode: string; verificationUri: string; verificationUriComplete: string; interval: number; expiresIn: number }
export interface CoreInfo { installed: boolean; version: string; sha256: string; state: 'missing' | 'checking' | 'downloading' | 'verifying' | 'ready' | 'failed'; progress: number; downloadedBytes: number; totalBytes: number; error?: string }
export interface Release { id: number; name: string; slug: string }
export interface Asset { id: number; filename: string; checksum: string; available_sources: { source: string; name: string }[] }

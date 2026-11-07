<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { ElConfigProvider, ElMessage, ElMessageBox } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import AppIcon from './AppIcon.vue'
import TrafficChart from './TrafficChart.vue'
import { request, RequestError, type Account, type CoreInfo, type Device, type Domain, type Node, type Notice, type Overview, type Process, type Session, type Settings, type Tunnel } from './api'

const AnnouncementList = defineAsyncComponent(() => import('./AnnouncementList.vue'))

type Page = 'overview' | 'tunnels' | 'nodes' | 'domains' | 'processes' | 'settings'
const pages: { id: Page; label: string; title: string; icon: string }[] = [
  { id: 'overview', label: '概览', title: '数据概览', icon: 'home' },
  { id: 'tunnels', label: '隧道管理', title: '隧道管理', icon: 'server' },
  { id: 'nodes', label: '节点列表', title: '节点列表', icon: 'nodes' },
  { id: 'domains', label: '域名管理', title: '域名管理', icon: 'globe' },
  { id: 'processes', label: '运行日志', title: '运行日志', icon: 'file' },
  { id: 'settings', label: '设置', title: '应用设置', icon: 'settings' },
]
const navigation = pages.filter(p => p.id !== 'processes')
const page = ref<Page>('overview')
const current = computed(() => pages.find(p => p.id === page.value)!)
const session = ref<Session>()
const settings = ref<Settings>()
const info = ref<Overview>()
const core = ref<CoreInfo>()
const tunnels = ref<Tunnel[]>([])
const nodes = ref<Node[]>([])
const domains = ref<Domain[]>([])
const domainPage = ref(1)
const domainTotal = ref(0)
const processes = ref<Record<string, Process>>({})
const preferences = ref<Record<string, boolean>>({})
const loading = ref(true)
const refreshing = ref(false)
const busy = ref('')
const error = ref('')
const query = ref('')
const device = ref<Device>()
const deviceExpires = ref(0)
const now = ref(Date.now())
const remaining = computed(() => Math.max(0, Math.ceil((deviceExpires.value - now.value) / 1000)))
const account = computed<Account | undefined>(() => info.value?.account ?? session.value?.account)
const avatarURL = computed(() => actionLink(account.value?.avatar))
const avatarFailed = ref(false)
watch(avatarURL, () => { avatarFailed.value = false })
const filteredTunnels = computed(() => tunnels.value.filter(t => `${t.remark} ${t.name} ${t.node_name} ${t.local_ip}`.toLowerCase().includes(query.value.toLowerCase())))
const traffic = computed(() => info.value?.stats ?? account.value)
const trafficRemaining = computed(() => Math.max(0, info.value?.stats?.traffic_remaining ?? account.value?.traffic_limit ?? 0))
// The platform reports traffic_limit as remaining credit, rather than a fixed cap.
const trafficTotal = computed(() => trafficRemaining.value + Math.max(0, traffic.value?.traffic_used ?? 0))
const remainingPercent = computed(() => trafficTotal.value ? Math.min(100, trafficRemaining.value / trafficTotal.value * 100) : 0)
const todayTraffic = computed(() => info.value?.daily?.daily_stats?.find(d => d.date.slice(0, 10) === localDate(now.value))?.total_traffic ?? 0)
const greeting = computed(() => { const hour = new Date(now.value).getHours(); return hour < 6 ? '夜深了' : hour < 12 ? '上午好' : hour < 18 ? '下午好' : '晚上好' })
const topNotices = ref<Notice[]>([])
const topNoticeError = ref('')
const lastTopRefresh = ref(0)
const lastOverviewRefresh = ref(0)
const selectedLog = ref('')
const log = computed(() => processes.value[selectedLog.value])
const processList = computed(() => Object.values(processes.value))
const coreBusy = computed(() => ['checking', 'downloading', 'verifying'].includes(core.value?.state ?? ''))
const coreRequestBusy = ref(false)
const coreDialog = ref(false)
const coreStage = computed(() => ({ missing: '正在准备隧道内核', checking: '正在获取内核版本', downloading: '正在下载隧道内核', verifying: '正在校验并安装内核', ready: '隧道内核已就绪', failed: '内核下载失败' }[core.value?.state ?? 'missing']))
watch(() => [session.value?.authenticated, core.value?.installed, coreBusy.value, core.value?.state === 'failed'] as const, ([authenticated, installed, installing, failed], previous) => {
  if (!authenticated || (installed && !installing && !failed)) coreDialog.value = false
  else if (authenticated !== previous?.[0] || (installing && !previous?.[2]) || (failed && !previous?.[3])) coreDialog.value = true
})
let corePollTimer: ReturnType<typeof setInterval> | undefined
let coreRefreshing = false
let pollTimer: ReturnType<typeof setTimeout> | undefined
let refreshTimer: ReturnType<typeof setInterval> | undefined
let clockTimer: ReturnType<typeof setInterval> | undefined
let disposed = false

function localDate(timestamp: number) { const date = new Date(timestamp); return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}` }
function noticeType(level: string): 'info' | 'warning' | 'error' | 'success' { return ['warning', 'error', 'success'].includes(level) ? level as 'warning' | 'error' | 'success' : 'info' }
function actionLink(raw?: string) { try { const url = new URL(raw ?? ''); return ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password ? url.href : '' } catch { return '' } }
async function loadTopNotices(force = false) {
  if (!force && Date.now() - lastTopRefresh.value < 60000) return
  try { topNotices.value = (await request<{ items: Notice[] }>('/announcements/top')).items; topNoticeError.value = ''; lastTopRefresh.value = Date.now() }
  catch (e) { topNoticeError.value = `顶部公告加载失败：${message(e)}` }
}
function message(e: unknown) { return e instanceof Error ? e.message : String(e) }
async function act(key: string, fn: () => Promise<void>) {
  if (busy.value) return
  busy.value = key
  try { await fn() } catch (e) { if (e !== 'cancel' && e !== 'close') ElMessage.error(message(e)) } finally { busy.value = '' }
}
function bytes(value = 0) {
  if (value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']; const i = Math.min(Math.floor(Math.log(value) / Math.log(1024)), 4)
  return `${(value / 1024 ** i).toFixed(i === 0 ? 0 : 2)} ${units[i]}`
}
function state(v?: string) { return ({ running: '运行中', starting: '连接中', stopping: '停止中', stopped: '已停止', failed: '启动失败' } as Record<string, string>)[v ?? 'stopped'] ?? v }
function isRunning(t: Tunnel) { return ['running', 'starting'].includes(processes.value[t.name]?.state ?? '') }
function address(t: Tunnel) { return t.custom_domain ? `${t.type}://${t.custom_domain}` : `${t.node_address}:${t.remote_port}` }
async function copy(value: string) { try { await navigator.clipboard.writeText(value); ElMessage.success('已复制') } catch { ElMessage.info('请选中文本手动复制') } }
function verifyLink() {
  const raw = device.value?.verificationUriComplete || device.value?.verificationUri || 'https://dash.lolia.link/oauth/device'
  try { const u = new URL(raw); return u.protocol === 'https:' ? u.href : 'https://dash.lolia.link/oauth/device' } catch { return 'https://dash.lolia.link/oauth/device' }
}
async function init() {
  loading.value = true; error.value = ''
  try {
    const [s, cfg, c] = await Promise.all([request<Session>('/session'), request<Settings>('/settings'), request<CoreInfo>('/core/status')])
    session.value = s; settings.value = cfg; core.value = c
    if (s.authenticated) { await loadPage(); if (!core.value?.installed && !coreBusy.value && core.value?.state !== 'failed') void prepareCore() }
  } catch (e) { error.value = message(e) } finally { loading.value = false }
}
async function loadTunnels() {
  const data = await request<{ items: Tunnel[]; processes: Record<string, Process>; preferences: Record<string, boolean> }>('/tunnels')
  tunnels.value = data.items; processes.value = data.processes; preferences.value = data.preferences
}
async function loadPage(forceNotices = false) {
  if (!session.value?.authenticated || refreshing.value) return
  refreshing.value = true; error.value = ''
  try {
    if (page.value === 'overview') { const [data] = await Promise.all([request<Overview>('/overview'), loadTunnels(), loadTopNotices(forceNotices)]); info.value = data; lastOverviewRefresh.value = Date.now() }
    if (page.value === 'tunnels') await loadTunnels()
    if (page.value === 'nodes') nodes.value = (await request<{ nodes: Node[] }>('/nodes')).nodes
    if (page.value === 'domains') { const data = await request<{ domains: Domain[]; total: number }>(`/domains?page=${domainPage.value}&limit=10`); domains.value = data.domains; domainTotal.value = data.total }
    if (page.value === 'processes') { processes.value = await request<Record<string, Process>>('/processes'); if (!selectedLog.value || !processes.value[selectedLog.value]) selectedLog.value = Object.keys(processes.value)[0] ?? '' }
    if (page.value === 'settings') { settings.value = await request<Settings>('/settings'); core.value = await request<CoreInfo>('/core/status') }
  } catch (e) { error.value = message(e) } finally { refreshing.value = false }
}
async function navigate(value: Page) { page.value = value; query.value = ''; while (refreshing.value && !disposed) await new Promise(resolve => setTimeout(resolve, 25)); if (!disposed) await loadPage() }
async function openLogs(t: Tunnel) { selectedLog.value = t.name; await navigate('processes') }
async function beginLogin() {
  error.value = ''
  await act('login', async () => {
    device.value = await request<Device>('/auth/login', 'POST'); deviceExpires.value = Date.now() + device.value.expiresIn * 1000
    schedulePoll(device.value.interval)
  })
}
function schedulePoll(seconds: number) { clearTimeout(pollTimer); if (!disposed) pollTimer = setTimeout(poll, Math.max(5, seconds) * 1000) }
async function poll() {
  if (disposed || !device.value || !remaining.value) { device.value = undefined; return }
  try {
    const result = await request<{ status: string; interval?: number }>('/auth/login/poll', 'POST')
    if (result.status === 'authenticated') { device.value = undefined; ElMessage.success('账号绑定成功'); await init(); return }
    if (result.status === 'access_denied' || result.status === 'expired_token') { device.value = undefined; ElMessage.warning(result.status === 'access_denied' ? '你已拒绝授权' : '设备码已过期，请重新申请'); return }
    schedulePoll(result.interval ?? device.value.interval)
  } catch (e) { error.value = message(e); if (e instanceof RequestError && e.status >= 400 && e.status < 500) device.value = undefined; else schedulePoll((device.value?.interval ?? 5) + 5) }
}
async function logout() { await act('logout', async () => { await ElMessageBox.confirm('解绑后将停止当前账号在本机的全部隧道，并移除保存的授权。', '解绑账号', { type: 'warning', confirmButtonText: '解绑', cancelButtonText: '取消' }); const result = await request<{ warning: string }>('/auth/logout', 'POST'); if (result.warning) ElMessage.warning(result.warning); clearTimeout(pollTimer); device.value = undefined; info.value = undefined; tunnels.value = []; processes.value = {}; topNotices.value = []; topNoticeError.value = ''; lastTopRefresh.value = 0; page.value = 'overview'; await init() }) }
async function bootChanged(value: string | number | boolean) { await act('boot', async () => { await request('/settings', 'PUT', { bootAutoStart: !!value }); settings.value = await request<Settings>('/settings') }) }
async function toggleTunnel(t: Tunnel) { await act(t.name, async () => { const p = processes.value[t.name]; const running = p && ['starting', 'running'].includes(p.state); await request(`/tunnels/${t.name}/${running ? 'stop' : 'start'}`, 'POST'); await loadTunnels() }) }
async function autoStart(t: Tunnel, value: string | number | boolean) { await act(`auto-${t.name}`, async () => { await request(`/tunnels/${t.name}/auto-start`, 'PUT', { enabled: !!value }); await loadTunnels() }) }
async function removeTunnel(t: Tunnel) { await act(`delete-${t.name}`, async () => { await ElMessageBox.confirm(`删除「${t.remark || t.name}」后将停止本机进程，公网映射也会被移除。`, '删除隧道', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }); await request(`/tunnels/${t.name}`, 'DELETE'); await loadTunnels(); ElMessage.success('隧道已删除') }) }
const dialog = ref(false)
const editing = ref<Tunnel>()
const form = reactive({ node_id: 0, type: 'tcp', local_ip: '127.0.0.1', local_port: 8080, remote_port: 0, custom_domain: '', remark: '', auto_tls: false, http_redirect: false, protocol: '', proxy_protocol_version: '' })
const selectedNode = computed(() => nodes.value.find(n => n.id === form.node_id))
watch(() => form.node_id, () => {
  if (editing.value) return
  const protocols = selectedNode.value?.supported_protocols
  if (protocols?.length && !protocols.includes(form.type)) {
    form.type = protocols[0]
  }
})
async function openTunnel(t?: Tunnel) {
  await act('form', async () => {
    if (!t) nodes.value = (await request<{ nodes: Node[] }>('/nodes')).nodes
    editing.value = t
    const defaultNode = !t ? (nodes.value.find(n => n.status === 'online') ?? nodes.value[0]) : undefined
    const defaultType = !t
      ? (defaultNode?.supported_protocols?.includes('tcp') ? 'tcp' : (defaultNode?.supported_protocols?.[0] ?? 'tcp'))
      : (t?.type ?? 'tcp')
    Object.assign(form, { node_id: t?.node_id ?? defaultNode?.id ?? 0, type: defaultType, local_ip: t?.local_ip ?? '127.0.0.1', local_port: t?.local_port ?? 8080, remote_port: t?.remote_port ?? 0, custom_domain: t?.custom_domain ?? '', remark: t?.remark ?? '', auto_tls: t?.config?.auto_tls ?? false, http_redirect: t?.config?.http_redirect ?? false, protocol: t?.config?.protocol ?? '', proxy_protocol_version: t?.config?.proxy_protocol_version ?? '' })
    dialog.value = true
  })
}
async function saveTunnel() { await act('saveTunnel', async () => {
  if (!form.local_ip.trim() || form.local_port < 1 || form.local_port > 65535) throw new Error('请填写有效的本地 IP 和端口')
  if (['http', 'https'].includes(form.type) && !form.custom_domain.trim()) throw new Error('HTTP / HTTPS 隧道需要已验证的域名')
  const common = { local_ip: form.local_ip.trim(), local_port: form.local_port, remark: form.remark, ...(['http', 'https'].includes(form.type) ? { custom_domain: form.custom_domain.trim() } : {}) }
  if (editing.value) {
    const config: Record<string, unknown> = {
      protocol: form.protocol,
      proxy_protocol_version: form.proxy_protocol_version,
    }
    if (form.type === 'https') {
      config.auto_tls = form.auto_tls
      config.http_redirect = form.http_redirect
    }
    await request(`/tunnels/${editing.value.name}`, 'PUT', { ...common, config })
  } else {
    await request('/tunnels', 'POST', { ...common, node_id: form.node_id, type: form.type, ...(form.remote_port && !['http', 'https'].includes(form.type) ? { remote_port: form.remote_port } : {}) })
  }
  dialog.value = false; await loadTunnels(); ElMessage.success(editing.value ? '已保存，请重新启动隧道' : '隧道已创建')
}) }
const domainDialog = ref(false)
const domainInput = ref('')
async function addDomain() { await act('domain', async () => { if (!domainInput.value.trim()) throw new Error('请输入域名'); await request('/domains', 'POST', { domain: domainInput.value.trim() }); domainDialog.value = false; domainInput.value = ''; domainPage.value = 1; await loadPage(); ElMessage.success('域名已添加，请配置 DNS TXT 记录') }) }
async function verifyDomain(d: Domain) { await act(`verify-${d.id}`, async () => { await request('/domains/verify', 'POST', { domain: d.domain }); await loadPage(); ElMessage.success('域名验证成功') }) }
async function removeDomain(d: Domain) { await act(`domain-${d.id}`, async () => { await ElMessageBox.confirm(`确定移除 ${d.domain}？正在被隧道使用的域名需要先解除关联。`, '移除域名', { confirmButtonText: '移除', cancelButtonText: '取消' }); await request(`/domains/${d.id}`, 'DELETE'); await loadPage() }) }
async function prepareCore(update = false) {
  if (coreRequestBusy.value || coreBusy.value || !session.value?.authenticated) return
  coreRequestBusy.value = true
  try {
    if (update && core.value?.installed) await ElMessageBox.confirm('将安装平台推荐内核。请先停止所有用户在本机运行的隧道。', '更新内核', { confirmButtonText: '更新', cancelButtonText: '取消' })
    coreDialog.value = true
    core.value = await request<CoreInfo>(update ? '/core/update' : '/core/setup', 'POST')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') { core.value = { installed: core.value?.installed ?? false, version: core.value?.version ?? '', sha256: core.value?.sha256 ?? '', state: 'failed', progress: 0, downloadedBytes: 0, totalBytes: 0, error: message(e) } }
  } finally { coreRequestBusy.value = false }
}
async function refreshCore() {
  if (coreRefreshing || !session.value?.authenticated) return
  coreRefreshing = true
  try {
    const wasInstalling = coreBusy.value
    const status = await request<CoreInfo>('/core/status')
    core.value = status
    if (wasInstalling && status.state === 'ready') ElMessage.success('隧道内核已就绪')
  } catch (e) { if (core.value) core.value.error = `无法获取内核状态：${message(e)}` }
  finally { coreRefreshing = false }
}
onMounted(async () => { clockTimer = setInterval(() => { now.value = Date.now() }, 1000); await init(); corePollTimer = setInterval(() => { if (session.value?.authenticated && !document.hidden && (coreBusy.value || !core.value?.installed)) void refreshCore() }, 2000); refreshTimer = setInterval(() => { if (session.value?.authenticated && !busy.value && !dialog.value && !document.hidden && (['tunnels', 'processes'].includes(page.value) || (page.value === 'overview' && Date.now() - lastOverviewRefresh.value >= 60000))) void loadPage() }, 5000) })
onUnmounted(() => { disposed = true; clearTimeout(pollTimer); clearInterval(refreshTimer); clearInterval(clockTimer); clearInterval(corePollTimer) })
</script>

<template>
  <el-config-provider :locale="zhCn">
    <div class="app-frame">
      <aside class="sidebar">
        <nav aria-label="主导航">
          <template v-if="session?.authenticated">
            <button v-for="item in navigation" :key="item.id" :class="{ active: page === item.id }" :aria-current="page === item.id ? 'page' : undefined" :aria-label="item.label" :title="item.label" @click="navigate(item.id)">
              <AppIcon :name="item.icon" /><span>{{ item.label }}</span>
            </button>
          </template>
          <button v-else class="active" title="登录"><AppIcon name="login" /><span>登录</span></button>
          <a href="https://docs.lolia.link" target="_blank" rel="noopener noreferrer" title="帮助中心"><AppIcon name="help" /><span>帮助中心</span></a>
        </nav>
      </aside>
      <main class="content">
        <section v-if="session?.authenticated && page === 'overview'" class="top-announcements" aria-label="顶部公告" aria-live="polite">
          <el-alert effect="light" v-for="n in topNotices" :key="n.id" :title="n.title" :type="noticeType(n.level)" :closable="false" show-icon class="top-notice">
            <p>{{ n.content }}</p>
            <a v-if="n.action_label && actionLink(n.action_url)" :href="actionLink(n.action_url)" target="_blank" rel="noopener noreferrer" class="notice-action">{{ n.action_label }}</a>
          </el-alert>
          <el-alert effect="light" v-if="topNoticeError" :title="topNoticeError" type="warning" :closable="false"><template #default><el-button text @click="loadTopNotices(true)">重试</el-button></template></el-alert>
        </section>
        <div v-if="session?.authenticated && page !== 'overview'" class="page-head">
          <h1>{{ current.title }}</h1>
          <div class="page-actions">
            <el-button v-if="page === 'tunnels'" type="primary" :disabled="!!busy" @click="openTunnel()">创建隧道</el-button>
            <el-button :loading="refreshing" @click="loadPage(true)">刷新</el-button>
          </div>
        </div>
        <el-alert effect="light" v-if="error" :title="error" type="error" show-icon :closable="false" class="page-alert"><template #default><el-button text @click="init">重新加载</el-button></template></el-alert>
        <el-skeleton v-if="loading" :rows="7" animated />
        <template v-else>
          <div v-if="!session?.authenticated" class="login-wrap">
            <section class="card login-card">
              <div class="login-title">
                <h1>{{ device && remaining ? '授权登录' : '欢迎登录' }}</h1>
                <p>{{ device && remaining ? '打开授权页面，输入设备码即可完成登录。' : '在浏览器中完成 LoliaFRP 设备授权登录。' }}</p>
              </div>
              <el-button v-if="!device || !remaining" type="primary" size="large" class="login-button" :loading="busy === 'login'" @click="beginLogin">{{ device ? '重新申请设备码' : '从浏览器进行登录' }}</el-button>
              <div v-else class="device-panel">
                <button class="device-code" aria-label="复制设备码" @click="copy(device.userCode)">{{ device.userCode }}</button>
                <span class="device-code-hint">点击设备码复制</span>
                <a :href="verifyLink()" target="_blank" rel="noopener noreferrer" class="authorize-link">打开授权页面</a>
                <p class="device-waiting">等待授权完成 · 剩余 {{ remaining }} 秒</p>
              </div>
            </section>
          </div>
          <template v-else-if="page === 'overview' && info">
            <header class="overview-header">
              <div class="account-heading"><span class="account-avatar"><img v-if="avatarURL && !avatarFailed" :key="avatarURL" :src="avatarURL" :alt="`${account?.username ?? ''} 的头像`" referrerpolicy="no-referrer" @error="avatarFailed = true"><AppIcon v-else name="user" /></span><div><h1>{{ greeting }}，{{ account?.username }}</h1><p>{{ account?.email || `用户 #${account?.id}` }}</p></div></div>
              <div class="overview-actions"><span class="connection-state"><i></i>账号已连接<small>本机运行 {{ processList.filter(p => p.state === 'running').length }} 条隧道</small></span><el-button :loading="refreshing" @click="loadPage(true)">刷新</el-button></div>
            </header>
            <el-alert effect="light" v-if="account?.is_banned || !account?.has_qq" :title="account?.is_banned ? '账号已被封禁，请联系平台处理。' : '创建或启动隧道前，请先在 LoliaFRP 控制台绑定 QQ。'" type="warning" :closable="false" show-icon class="page-alert" />
            <div class="overview-grid">
              <section class="card traffic-card">
                <h2 class="card-heading"><AppIcon name="traffic" />流量概览</h2>
                <div class="traffic-balance"><div><span>剩余流量</span><strong>{{ bytes(trafficRemaining) }}</strong></div><div class="traffic-total"><span>总流量</span><strong>{{ bytes(trafficTotal) }}</strong></div></div>
                <el-progress :percentage="remainingPercent" :show-text="false" :stroke-width="14" />
                <div class="traffic-details"><div><span>已用流量</span><strong>{{ bytes(traffic?.traffic_used) }}</strong></div><div><span>剩余百分比</span><strong>{{ remainingPercent.toFixed(1) }}<small>%</small></strong></div></div>
              </section>
              <section class="card notice-card" aria-label="公告板">
                <h2 class="card-heading"><AppIcon name="info" />公告板</h2>
                <div class="board-list">
                  <p v-if="info.warnings.announcements" class="board-error">公告板加载失败：{{ info.warnings.announcements }}</p>
                  <AnnouncementList :items="info.announcements?.items ?? []" />
                  <el-empty v-if="!info.announcements?.items.length && !info.warnings.announcements" description="暂无公告" :image-size="52" />
                </div>
              </section>
            </div>
            <section class="card trend-card">
              <div class="trend-heading"><h2 class="card-heading"><AppIcon name="chart" />流量趋势</h2><div>今日：<strong>{{ bytes(todayTraffic) }}</strong><span>近 7 天：<strong>{{ bytes(info.summary?.total_traffic) }}</strong></span></div></div>
              <TrafficChart v-if="info.daily?.daily_stats?.length" :days="info.daily.daily_stats" /><el-empty v-else description="暂无流量数据" :image-size="60" />
            </section>
            <el-alert effect="light" v-for="(warning, key) in info.warnings" v-show="key !== 'announcements'" :key="key" :title="`${key}：${warning}`" type="warning" show-icon :closable="false" class="page-alert" />
          </template>
          <section v-else-if="page === 'tunnels'">
            <div class="tunnel-toolbar"><el-input v-model="query" placeholder="搜索备注、节点或本地地址" clearable class="search-input" /><span class="muted">共 {{ tunnels.length }} 条隧道</span></div>
            <div v-if="filteredTunnels.length" class="tunnel-grid">
              <article v-for="t in filteredTunnels" :key="t.name" class="card tunnel-card" :class="{ running: isRunning(t) }">
                <div class="tunnel-head">
                  <div class="tunnel-identity">
                    <span class="tunnel-icon"><AppIcon name="server" /></span>
                    <div class="tunnel-title"><h2 :title="t.remark || t.name">{{ t.remark || t.name.slice(0, 12) }}</h2><span class="protocol">{{ t.type.toUpperCase() }}</span></div>
                  </div>
                  <el-button size="small" :type="isRunning(t) ? 'default' : 'primary'" :loading="busy === t.name" :disabled="!!busy || processes[t.name]?.state === 'stopping' || (!isRunning(t) && (!core?.installed || coreBusy))" @click="toggleTunnel(t)">{{ isRunning(t) ? '停止' : '启动' }}</el-button>
                </div>
                <div class="tunnel-info">
                  <div class="info-row"><span>节点</span><strong>{{ t.node_name || `节点 ${t.node_id}` }}</strong></div>
                  <div class="info-row"><span>本地地址</span><strong class="mono">{{ t.local_ip }}:{{ t.local_port }}</strong></div>
                  <div class="info-row"><span>{{ t.custom_domain ? '域名' : '远程地址' }}</span><button class="text-copy mono" :title="address(t)" @click="copy(address(t))">{{ address(t) }}</button></div>
                  <div class="info-row"><span>运行状态</span><strong><span :class="{ 'running-text': isRunning(t) }">本机{{ state(processes[t.name]?.state) }}</span><span class="muted"> · 平台{{ t.status === 'active' ? '在线' : '离线' }}</span></strong></div>
                </div>
                <div class="tunnel-footer">
                  <label class="auto-start"><span>自动启动</span><el-switch :model-value="preferences[t.name] ?? false" :disabled="!!busy" :aria-label="`${t.remark || t.name} 自动启动`" @change="autoStart(t, $event)" /></label>
                  <div><el-button text size="small" @click="openLogs(t)">日志</el-button><el-button text size="small" :disabled="!!busy" @click="openTunnel(t)">编辑</el-button><el-button text type="danger" size="small" :disabled="!!busy" @click="removeTunnel(t)">删除</el-button></div>
                </div>
              </article>
            </div>
            <el-empty v-else :description="query ? '没有匹配的隧道' : '暂无隧道'" />
          </section>
          <section v-else-if="page === 'nodes'" class="nodes-grid"><article v-for="n in nodes" :key="n.id" class="panel node-card"><div class="section-heading"><span class="node-region">{{ n.region_code || 'NET' }}</span><el-tag :type="n.status === 'online' ? 'success' : 'info'">{{ n.status === 'online' ? '在线' : n.status === 'authenticated' ? '已认证' : '离线' }}</el-tag></div><h3>{{ n.name }}</h3><div class="node-meta"><span>{{ n.bandwidth }} Mbps</span><span>{{ n.traffic_ratio }}× 流量倍率</span></div><el-progress v-if="n.load !== undefined" :percentage="Math.min(100, Math.max(0, n.load))" :stroke-width="5" /><p v-else class="muted">暂无负载报告</p><div class="node-tags"><el-tag v-for="p in n.supported_protocols" :key="p" type="info" size="small">{{ p.toUpperCase() }}</el-tag><el-tag v-if="n.need_kyc" type="warning" size="small">需实名</el-tag><el-tag v-if="n.beian_required" type="warning" size="small">需备案</el-tag></div></article><el-empty v-if="!nodes.length" description="暂无可用节点" /></section>
          <section v-else-if="page === 'domains'" class="panel table-panel"><div class="table-toolbar"><div><h3>我的域名</h3><p class="muted">验证通过后，域名及其子域名可用于 HTTP / HTTPS 隧道。</p></div><el-button type="primary" @click="domainDialog = true">＋ 添加域名</el-button></div><el-table :data="domains" row-key="id" empty-text="暂无域名"><el-table-column type="expand"><template #default="{ row }"><div class="dns-detail" v-if="row.verification_subdomain"><p>在域名 DNS 中添加 TXT 记录，再点击验证。</p><p>记录名：<code>{{ row.verification_subdomain }}</code> <el-button link @click="copy(row.verification_subdomain)">复制</el-button></p><p>记录值：<code>{{ row.verification_token }}</code> <el-button link @click="copy(row.verification_token)">复制</el-button></p><p>有效期至 {{ row.expires_at }}</p></div><p v-else class="dns-detail">已完成验证或验证信息已过期；过期时请移除后重新添加。</p></template></el-table-column><el-table-column prop="domain" label="域名" min-width="180" /><el-table-column label="状态" width="140"><template #default="{ row }"><el-tag :type="row.is_verified ? 'success' : 'warning'">{{ row.is_verified ? '已验证' : '待验证' }}</el-tag></template></el-table-column><el-table-column label="备案" width="100"><template #default="{ row }">{{ row.has_beian ? '已备案' : '未备案' }}</template></el-table-column><el-table-column label="操作" min-width="150"><template #default="{ row }"><el-button v-if="!row.is_verified" text type="primary" :disabled="!!busy" @click="verifyDomain(row)">验证</el-button><el-button text type="danger" :disabled="!!busy" @click="removeDomain(row)">移除</el-button></template></el-table-column></el-table><el-pagination class="pagination" v-model:current-page="domainPage" :page-size="10" :total="domainTotal" layout="prev, pager, next" @current-change="loadPage()" /></section>
          <section v-else-if="page === 'processes'" class="panel logs-panel"><div class="table-toolbar"><div><h3>本机运行日志</h3><p class="muted">每条隧道保留最近 300 行日志，自动刷新。</p></div><el-select v-model="selectedLog" placeholder="选择隧道" class="log-select"><el-option v-for="p in processList" :key="p.name" :value="p.name" :label="tunnels.find(t => t.name === p.name)?.remark || p.name" /></el-select></div><div v-if="log" class="log-meta"><el-tag :type="log.state === 'running' ? 'success' : 'info'">{{ state(log.state) }}</el-tag><span>PID {{ log.pid || '—' }} · 重启 {{ log.restarts }} 次</span></div><el-alert effect="light" v-if="log?.error" :title="log.error" type="error" :closable="false" /><pre v-if="log" class="log-output">{{ log.logs.join('\n') || '等待内核输出日志…' }}</pre><el-empty v-else description="启动隧道后，可以在这里查看日志" /></section>
          <div v-else-if="page === 'settings'" class="settings-stack">
            <section class="card settings-section">
              <h2>应用信息</h2>
              <div class="facts">
                <div class="fact"><span>应用版本</span><strong>{{ settings?.version }}</strong></div>
                <div class="fact"><span>设备架构</span><strong>{{ settings?.arch }}</strong></div>
              </div>
              <div class="setting-row"><div><strong>{{ account?.username }}</strong><p>{{ account?.email }}</p></div><el-button type="danger" plain :loading="busy === 'logout'" @click="logout">解绑账号</el-button></div>
            </section>
            <section class="card settings-section">
              <h2>启动设置</h2>
              <div class="setting-row"><div><strong>应用启动时恢复隧道</strong><p>自动恢复已勾选「自动启动」的隧道。NAS 重启后恢复，还需在飞牛应用中心开启本应用的「开机自启」。</p></div><el-switch :model-value="settings?.bootAutoStart ?? false" :disabled="!!busy" aria-label="应用启动时恢复隧道" @change="bootChanged" /></div>
            </section>
            <section class="card settings-section">
              <h2>LoliaFRP 内核</h2>
              <div class="fact"><span>已安装版本</span><el-tag :type="core?.installed ? 'success' : 'warning'">{{ core?.installed ? core.version : '尚未安装' }}</el-tag></div>
              <p class="muted core-hint">首次登录后自动下载本机内核，已安装的内核会在应用升级后继续保留。</p>
              <div v-if="coreBusy" class="core-actions"><el-button @click="coreDialog = true">查看下载进度</el-button></div>
              <div v-else-if="!core?.installed" class="core-actions"><el-button :loading="coreRequestBusy" @click="prepareCore()">{{ core?.state === 'failed' ? '重试下载' : '下载内核' }}</el-button></div>
              <div v-else-if="session?.user.isAdmin" class="core-actions"><el-button :loading="coreRequestBusy" @click="prepareCore(true)">更新推荐内核</el-button></div>
              <p v-else-if="!session?.user.isAdmin" class="settings-footnote">共享内核的更新由飞牛管理员操作。</p>
            </section>
          </div>
        </template>
      </main>
    <el-dialog v-model="coreDialog" :title="coreStage" width="420px" :close-on-click-modal="false" class="core-download-dialog">
      <div class="core-download" role="status" aria-label="内核下载" aria-live="polite">
        <el-alert effect="light" v-if="core?.error" :title="core.error" type="error" :closable="false" show-icon />
        <p v-else class="muted">{{ core?.installed ? '正在更新内核，请稍候。' : '正在下载适合本机的隧道内核，完成后即可启动隧道。' }}</p>
        <el-progress v-if="coreBusy" :percentage="core?.progress ?? 0" :stroke-width="8" :indeterminate="!core?.totalBytes" />
        <small v-if="core?.totalBytes">{{ bytes(core.downloadedBytes) }} / {{ bytes(core.totalBytes) }}</small>
      </div>
      <template #footer>
        <el-button @click="coreDialog = false">{{ coreBusy ? '后台下载' : '关闭' }}</el-button>
        <el-button v-if="core?.state === 'failed' && (!core.installed || session?.user.isAdmin)" type="primary" :loading="coreRequestBusy" @click="prepareCore(core.installed)">重试下载</el-button>
      </template>
    </el-dialog>
    <el-dialog v-model="dialog" :title="editing ? '编辑隧道' : '创建隧道'" width="540px" :close-on-click-modal="false">
      <el-form label-position="top" @submit.prevent="saveTunnel"><div class="form-grid"><el-form-item label="节点" v-if="!editing"><el-select v-model="form.node_id" style="width:100%"><el-option v-for="n in nodes" :key="n.id" :value="n.id" :label="`${n.name}${n.need_kyc ? ' · 需实名' : ''}`" :disabled="n.status !== 'online'" /></el-select></el-form-item><el-form-item label="协议" v-if="!editing"><el-select v-model="form.type"><el-option v-for="p in selectedNode?.supported_protocols ?? ['tcp', 'udp', 'http', 'https']" :key="p" :value="p" :label="p.toUpperCase()" /></el-select></el-form-item><el-form-item label="本地 IP"><el-input v-model="form.local_ip" placeholder="127.0.0.1" /></el-form-item><el-form-item label="本地端口"><el-input-number v-model="form.local_port" :min="1" :max="65535" controls-position="right" /></el-form-item></div><el-form-item v-if="['http', 'https'].includes(form.type)" label="自定义域名"><el-input v-model="form.custom_domain" placeholder="须在域名管理中验证，不支持泛域名" maxlength="256" /></el-form-item><el-form-item v-else-if="!editing" label="远程端口（0 表示自动分配）"><el-input-number v-model="form.remote_port" :min="0" :max="65535" controls-position="right" /></el-form-item><el-form-item label="备注"><el-input v-model="form.remark" maxlength="500" placeholder="例如：我的 NAS 网站" /></el-form-item><template v-if="editing"><div class="form-grid"><el-form-item label="传输协议"><el-select v-model="form.protocol"><el-option label="默认" value="" /><el-option label="TCP" value="tcp" /><el-option label="KCP" value="kcp" /></el-select></el-form-item><el-form-item label="Proxy Protocol"><el-select v-model="form.proxy_protocol_version"><el-option label="关闭" value="" /><el-option label="v1" value="v1" /><el-option label="v2" value="v2" /></el-select></el-form-item></div><div v-if="form.type === 'https'" class="tls-options"><el-checkbox v-model="form.auto_tls">自动 TLS</el-checkbox><el-checkbox v-model="form.http_redirect">HTTP 跳转 HTTPS</el-checkbox></div></template><el-alert effect="light" v-else title="创建后可通过编辑设置 Auto TLS 和高级配置。" type="info" :closable="false" /></el-form><template #footer><el-button @click="dialog = false">取消</el-button><el-button type="primary" :loading="busy === 'saveTunnel'" @click="saveTunnel">{{ editing ? '保存修改' : '创建隧道' }}</el-button></template>
    </el-dialog>
    <el-dialog v-model="domainDialog" title="添加域名" width="460px"><el-form label-position="top" @submit.prevent="addDomain"><el-form-item label="域名"><el-input v-model="domainInput" placeholder="example.com" maxlength="253" /></el-form-item><p class="muted">添加后展开域名记录，按提示配置 DNS TXT 记录。验证信息有效期为 24 小时。</p></el-form><template #footer><el-button @click="domainDialog = false">取消</el-button><el-button type="primary" :loading="busy === 'domain'" @click="addDomain">添加</el-button></template></el-dialog>
    </div>
  </el-config-provider>
</template>

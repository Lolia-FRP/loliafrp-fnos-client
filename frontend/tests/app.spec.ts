import { expect, test, type Page } from '@playwright/test'

const name = '1234567890abcdef1234567890abcdef'
const avatar = 'https://cdn.example.com/avatars/lolia.png'
const account = { id: 1, username: 'Lolia', email: 'demo@example.com', avatar, role: 'user', max_tunnel_count: 10, traffic_limit: 107374182400, traffic_used: 1073741824, has_qq: true, has_kyc: true }
const tunnel = { id: 12, name, type: 'tcp', node_id: 1, local_ip: '127.0.0.1', local_port: 22, remote_port: 20001, custom_domain: '', remark: '我的 SSH', status: 'inactive', node_name: '香港-01', node_address: 'hk.example.com', config: {} }

async function mock(page: Page, initialAuth = true, admin = true, coreScenario: 'installed' | 'missing' | 'failure' = 'installed') {
  let authenticated = initialAuth
  let running = false
  let auto = false
  let pollCount = 0
  let setupCount = 0
  let corePolls = 0
  let coreReady = coreScenario === 'installed'
  const mutations: { path: string; method: string; body: Record<string, unknown> }[] = []
  await page.route(avatar, route => route.fulfill({ contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#dde8f8"/><circle cx="32" cy="24" r="10" fill="#7396c8"/><path d="M12 64v-9a20 20 0 0 1 40 0v9" fill="#7396c8"/></svg>' }))
  await page.route('**/app/loliafrp/api/v1/**', async route => {
    const req = route.request(); const path = new URL(req.url()).pathname.split('/api/v1')[1]; const method = req.method()
    if (method !== 'GET') mutations.push({ path, method, body: req.postDataJSON() })
    let data: unknown = {}
    if (path === '/session') data = { authenticated, account, user: { uid: '1000', username: 'admin', isAdmin: admin } }
    if (path === '/settings') data = { bootAutoStart: true, version: '1.0.0-beta', arch: 'amd64' }
    if (path === '/core/status' || path === '/core/setup') {
      if (path === '/core/setup') { setupCount++; corePolls = 0 }
      let stage = coreReady ? 'ready' : 'missing'
      if (!coreReady && setupCount) {
        if (coreScenario === 'failure' && setupCount === 1) stage = 'failed'
        else if (path === '/core/setup') stage = 'checking'
        else if (++corePolls >= 2) { coreReady = true; stage = 'ready' }
        else stage = 'downloading'
      }
      data = { installed: coreReady, version: coreReady ? '0.69.2' : '', sha256: coreReady ? 'a'.repeat(64) : '', state: stage, progress: stage === 'downloading' ? 40 : coreReady ? 100 : 0, downloadedBytes: stage === 'downloading' ? 4000 : 0, totalBytes: stage === 'downloading' ? 10000 : 0, error: stage === 'failed' ? '下载源暂不可用' : '' }
    }
    if (path === '/announcements/top') data = { items: [{ id: 2, title: '节点维护通知', content: '今晚 23:00 起进行节点维护。\n现有连接可能短暂中断。', level: 'warning', action_label: '查看详情', action_url: 'https://docs.lolia.link' }] }
    if (path === '/auth/login') data = { userCode: 'ABCD-EFGH', verificationUri: 'https://dash.lolia.link/oauth/device', verificationUriComplete: 'https://dash.lolia.link/oauth/device?user_code=ABCD-EFGH', expiresIn: 600, interval: 5 }
    if (path === '/auth/login/poll') { pollCount++; if (pollCount === 1) data = { status: 'slow_down', interval: 10 }; else { authenticated = true; data = { status: 'authenticated' } } }
    if (path === '/overview') data = { account, stats: { traffic_limit: account.traffic_limit, traffic_used: account.traffic_used, traffic_remaining: account.traffic_limit }, summary: { total_in: 1024, total_out: 2048, total_traffic: 3072 }, daily: { daily_stats: Array.from({ length: 7 }, (_, i) => ({ date: `2026-09-${24 + i}T00:00:00+08:00`, total_traffic: (i + 1) * 1024 })) }, announcements: { items: [{ id: 1, title: '欢迎使用 LoliaFRP', content: '今天也是适合连接的一天。', level: 'normal' }] }, warnings: {} }
    if (path === '/tunnels' && method === 'GET') data = { items: [tunnel], preferences: { [name]: auto }, processes: { [name]: { name, state: running ? 'running' : 'stopped', pid: running ? 123 : 0, restarts: 0, logs: ['连接已建立'] } } }
    if (path === `/tunnels/${name}/start`) running = true
    if (path === `/tunnels/${name}/stop`) running = false
    if (path === `/tunnels/${name}/auto-start`) auto = req.postDataJSON().enabled
    if (path === '/nodes') data = { nodes: [{ id: 1, name: '香港-01', region_code: 'HK', status: 'online', supported_protocols: ['tcp', 'udp', 'http', 'https'], need_kyc: true, beian_required: false, load: 12, bandwidth: 100, traffic_ratio: 1 }] }
    if (path === '/domains' && method === 'GET') data = { domains: [{ id: 7, domain: 'example.com', is_verified: false, has_beian: false, status: 'pending', verification_subdomain: 'lolia_abcd.example.com', verification_token: 'txt_value', expires_at: '2026-10-02' }], total: 1 }
    if (path === '/processes') data = { [name]: { name, state: 'running', pid: 123, restarts: 0, logs: ['连接已建立', '等待请求'] } }
    if (path === '/core/releases') data = { items: [{ id: 1, slug: 'lolia_frp_cli', name: 'LoliaFRP CLI' }], total: 1 }
    if (path === '/core/assets') data = { items: [{ id: 2, filename: 'frpc_linux_amd64.tar.gz', checksum: 'b'.repeat(64), available_sources: [{ source: 'direct', name: '官方源' }] }] }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data }) })
  })
  return { mutations, polls: () => pollCount }
}

test('device code login honors slow_down and completes', async ({ page }) => {
  await page.clock.install()
  const state = await mock(page, false)
  await page.goto('/app/loliafrp/')
  await expect(page.getByText(/Client ID/i)).toHaveCount(0)
  await page.getByRole('button', { name: '从浏览器进行登录' }).click()
  await expect(page.getByRole('button', { name: '复制设备码' })).toContainText('ABCD-EFGH')
  await expect(page.getByRole('link', { name: /打开授权页面/ })).toHaveAttribute('href', /dash\.lolia\.link\/oauth\/device/)
  const firstPoll = page.waitForResponse(response => response.url().endsWith('/auth/login/poll'))
  await page.clock.fastForward(5100)
  await (await firstPoll).finished()
  await page.clock.runFor(50)
  await expect.poll(state.polls).toBe(1)
  await page.clock.fastForward(9000)
  expect(state.polls()).toBe(1)
  await page.clock.fastForward(1100)
  await expect(page.getByRole('heading', { name: /，Lolia$/, level: 1 })).toBeVisible()
})
test('tunnel starts, stops and saves auto-start preference', async ({ page }) => {
  const state = await mock(page)
  await page.goto('/app/loliafrp/')
  await page.getByRole('button', { name: /隧道管理/ }).first().click()
  await expect(page.getByText('我的 SSH', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '启动', exact: true }).click()
  await expect(page.getByRole('button', { name: '停止', exact: true })).toBeVisible()
  await page.getByRole('switch', { name: '我的 SSH 自动启动' }).locator('..').click()
  await expect.poll(() => state.mutations.some(m => m.path.endsWith('/auto-start') && m.body.enabled === true)).toBeTruthy()
  await page.getByRole('button', { name: '停止', exact: true }).click()
  await expect(page.getByRole('button', { name: '启动', exact: true })).toBeVisible()
})
test('create tunnel uses server API fields and automatic remote port', async ({ page }) => {
  const state = await mock(page)
  await page.goto('/app/loliafrp/')
  await page.getByRole('button', { name: /隧道管理/ }).first().click()
  await page.getByRole('button', { name: /创建隧道/ }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  await dialog.getByPlaceholder('例如：我的 NAS 网站').fill('我的网站')
  await dialog.getByRole('button', { name: '创建隧道', exact: true }).click()
  await expect(dialog).not.toBeVisible()
  const created = state.mutations.find(m => m.path === '/tunnels' && m.method === 'POST')!
  expect(created.body).toMatchObject({ node_id: 1, type: 'tcp', local_ip: '127.0.0.1', local_port: 8080, remark: '我的网站' })
  expect(created.body).not.toHaveProperty('remote_port')
  expect(created.body).not.toHaveProperty('config')
})
test('domain DNS record and process logs render as text', async ({ page }) => {
  await mock(page)
  await page.goto('/app/loliafrp/')
  await page.getByRole('button', { name: /域名管理/ }).click()
  await page.locator('.el-table__expand-icon').click()
  await expect(page.getByText('lolia_abcd.example.com', { exact: true })).toBeVisible()
  await expect(page.getByText('txt_value', { exact: true })).toBeVisible()
  await expect(page.getByRole('navigation', { name: '主导航' }).getByRole('button', { name: /日志/ })).toHaveCount(0)
  await page.getByRole('button', { name: '隧道管理', exact: true }).click()
  await page.getByRole('button', { name: '日志', exact: true }).click()
  await expect(page.locator('.log-output')).toContainText('连接已建立')
})
test('non-admin cannot see shared core installation controls', async ({ page }) => {
  const state = await mock(page, true, false)
  await page.goto('/app/loliafrp/')
  await page.getByRole('button', { name: /设置/ }).click()
  await expect(page.getByText('共享内核的更新由飞牛管理员操作。')).toBeVisible()
  await expect(page.getByRole('button', { name: '更新推荐内核' })).toHaveCount(0)
  expect(state.mutations.some(m => m.path === '/core/setup')).toBeFalsy()
  await expect(page.getByText(/Client ID/i)).toHaveCount(0)
})
test('overview fits desktop and narrow fnOS windows', async ({ page }) => {
  await mock(page)
  const errors: string[] = []; page.on('pageerror', e => errors.push(e.message))
  await page.setViewportSize({ width: 1280, height: 850 })
  await page.goto('/app/loliafrp/')
  await expect(page.getByRole('heading', { name: /，Lolia$/, level: 1 })).toBeVisible()
  await expect(page.getByRole('img', { name: 'Lolia 的头像' })).toHaveAttribute('src', avatar)
  await expect.poll(() => page.getByRole('img', { name: 'Lolia 的头像' }).evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0)).toBeTruthy()
  await expect(page.getByRole('heading', { name: '欢迎使用 LoliaFRP' })).toBeVisible()
  await page.screenshot({ path: '../build/preview-desktop.png', fullPage: true })
  await page.getByRole('button', { name: '隧道管理', exact: true }).click()
  await expect(page.getByRole('heading', { name: '我的 SSH' })).toBeVisible()
  await page.screenshot({ path: '../build/preview-tunnels.png', fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy()
  await page.getByRole('button', { name: '概览', exact: true }).click()
  await page.screenshot({ path: '../build/preview-mobile.png', fullPage: true })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy()
  expect(errors).toEqual([])
})

test('top announcements render independently only on the overview page', async ({ page }) => {
  await mock(page)
  await page.goto('/app/loliafrp/')
  const top = page.getByRole('region', { name: '顶部公告' })
  await expect(top.getByText('节点维护通知', { exact: true })).toBeVisible()
  await expect(top).toContainText('现有连接可能短暂中断。')
  await expect(top.getByRole('link', { name: '查看详情' })).toHaveAttribute('href', 'https://docs.lolia.link/')
  await expect(page.getByRole('heading', { name: '公告板' })).toBeVisible()
  await expect(page.getByRole('heading', { name: '欢迎使用 LoliaFRP' })).toBeVisible()
  const board = page.getByRole('region', { name: '公告板', exact: true })
  await expect(board.getByRole('heading', { name: '节点维护通知' })).toHaveCount(0)
  await expect(board.getByRole('list', { name: '公告列表' })).toBeVisible()
  await expect(board.locator('.el-alert')).toHaveCount(0)
  await page.getByRole('button', { name: '节点列表', exact: true }).click()
  await expect(top).toHaveCount(0)
  await page.getByRole('button', { name: '概览', exact: true }).click()
  await expect(top.getByText('节点维护通知', { exact: true })).toBeVisible()
})

test('board list renders Markdown and HTML while removing active content', async ({ page }) => {
  await mock(page)
  const content = [
    '## 更新内容',
    '',
    '**Markdown 加粗** 和 *斜体*，以及 `frpc`。',
    '',
    '- 第一项',
    '- 第二项',
    '',
    '[使用文档](https://docs.lolia.link)',
    '',
    '<p><strong>HTML 加粗</strong> <em>HTML 斜体</em></p>',
    '<table><tr><th>版本</th><td>1.0.0-beta</td></tr></table>',
    '<script>window.announcementInjected = true</script>',
    '<img src="/broken-announcement-image.png" onerror="window.announcementInjected = true">',
    '<a href="javascript:window.announcementInjected=true">危险链接</a>',
  ].join('\n')
  await page.route('**/api/v1/overview', route => route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data: {
    account, warnings: {}, announcements: { items: [{ id: 7, title: '客户端更新', content, level: 'normal', created_at: '2026-10-01T00:00:00Z' }] },
  } }) }))
  await page.route('**/broken-announcement-image.png', route => route.fulfill({ status: 404, body: '' }))
  const imageResponse = page.waitForResponse(response => response.url().endsWith('/broken-announcement-image.png'))
  await page.goto('/app/loliafrp/')
  const board = page.getByRole('region', { name: '公告板', exact: true })
  await expect(board.getByRole('heading', { name: '客户端更新' })).toBeVisible()
  await expect(board.getByRole('heading', { name: '更新内容' })).toBeVisible()
  await expect(board.locator('strong', { hasText: 'Markdown 加粗' })).toBeVisible()
  await expect(board.locator('em', { hasText: 'HTML 斜体' })).toBeVisible()
  await expect(board.getByRole('cell', { name: '1.0.0-beta' })).toBeVisible()
  await expect(board.getByRole('link', { name: '使用文档' })).toHaveAttribute('href', 'https://docs.lolia.link')
  await expect(board.getByRole('link', { name: '使用文档' })).toHaveAttribute('target', '_blank')
  await expect(board.getByRole('link', { name: '使用文档' })).toHaveAttribute('rel', 'noopener noreferrer')
  await expect(board.locator('script, [onerror], a[href^="javascript:"]')).toHaveCount(0)
  await expect(board.locator('.el-alert')).toHaveCount(0)
  await (await imageResponse).finished()
  expect(await page.evaluate(() => 'announcementInjected' in window)).toBeFalsy()
  await page.screenshot({ path: '../build/preview-announcement-markdown.png', fullPage: true })
})

test('top announcements show text safely and omit invalid action links', async ({ page }) => {
  await mock(page)
  await page.route('**/api/v1/announcements/top', route => route.fulfill({ contentType: 'application/json', body: JSON.stringify({ data: { items: [{ id: 3, title: '顶部提示', content: '<img src=x onerror="window.injected=true">', level: 'normal', action_label: '不安全链接', action_url: 'javascript:alert(1)' }] } }) }))
  await page.goto('/app/loliafrp/')
  const top = page.getByRole('region', { name: '顶部公告' })
  await expect(top).toContainText('<img src=x onerror="window.injected=true">')
  await expect(top.locator('img')).toHaveCount(0)
  await expect(top.getByRole('link')).toHaveCount(0)
  expect(await page.evaluate(() => 'injected' in window)).toBeFalsy()
})

test('failed top announcement request offers retry without hiding dashboard', async ({ page }) => {
  await mock(page)
  const pattern = '**/api/v1/announcements/top'
  await page.route(pattern, route => route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { message: '公告服务暂不可用' } }) }))
  await page.goto('/app/loliafrp/')
  await expect(page.getByText('顶部公告加载失败：公告服务暂不可用')).toBeVisible()
  await expect(page.getByRole('heading', { name: '流量概览' })).toBeVisible()
  await page.unroute(pattern)
  await page.getByRole('button', { name: '重试', exact: true }).click()
  await expect(page.getByRole('region', { name: '顶部公告' }).getByText('节点维护通知', { exact: true })).toBeVisible()
  await expect(page.getByText(/顶部公告加载失败/)).toHaveCount(0)
})


test('missing core downloads automatically after device login for a regular NAS user', async ({ page }) => {
  await page.clock.install()
  const state = await mock(page, false, false, 'missing')
  await page.goto('/app/loliafrp/')
  await expect(page.getByRole('button', { name: '从浏览器进行登录' })).toBeVisible()
  expect(state.mutations.some(m => m.path === '/core/setup')).toBeFalsy()
  await page.getByRole('button', { name: '从浏览器进行登录' }).click()
  const firstPoll = page.waitForResponse(response => response.url().endsWith('/auth/login/poll'))
  await page.clock.fastForward(5100)
  await (await firstPoll).finished()
  await page.clock.runFor(50)
  await expect.poll(state.polls).toBe(1)
  await page.clock.fastForward(10100)
  await expect.poll(() => state.mutations.filter(m => m.path === '/core/setup').length).toBe(1)
  const download = page.getByRole('dialog')
  await expect(download.getByRole('heading', { name: '正在获取内核版本' })).toBeVisible()
  await page.clock.fastForward(2100)
  await expect(download.getByRole('heading', { name: '正在下载隧道内核' })).toBeVisible()
  await expect(download.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '40')
  await page.screenshot({ path: '../build/preview-core-download.png', fullPage: true })
  await download.getByRole('button', { name: '后台下载', exact: true }).click()
  await expect(download).not.toBeVisible()
  await page.getByRole('button', { name: '隧道管理', exact: true }).click()
  await expect(page.getByRole('button', { name: '启动', exact: true })).toBeDisabled()
  await page.clock.fastForward(2100)
  await expect(page.getByRole('button', { name: '启动', exact: true })).toBeEnabled()
  await expect(download).not.toBeVisible()
  expect(state.mutations.filter(m => m.path === '/core/setup')).toHaveLength(1)
})

test('saved login bootstraps missing core and offers retry after failure', async ({ page }) => {
  await page.clock.install()
  const state = await mock(page, true, false, 'failure')
  await page.goto('/app/loliafrp/')
  await expect(page.getByText('下载源暂不可用')).toBeVisible()
  await page.getByRole('button', { name: '重试下载' }).click()
  await expect.poll(() => state.mutations.filter(m => m.path === '/core/setup').length).toBe(2)
  await page.clock.fastForward(2100)
  await expect(page.getByRole('heading', { name: '正在下载隧道内核' })).toBeVisible()
  await page.clock.fastForward(2100)
  await expect(page.getByRole('dialog')).not.toBeVisible()
  await page.getByRole('button', { name: '设置', exact: true }).click()
  await expect(page.getByText('0.69.2', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '更新推荐内核' })).toHaveCount(0)
})

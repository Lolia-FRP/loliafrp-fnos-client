<div align="center">

![](https://socialify.git.ci/Lolia-FRP/loliafrp-fnos-client/image?description=1&font=Inter&forks=1&issues=1&language=1&name=1&owner=1&pattern=Plus&pulls=1&stargazers=1&theme=Auto)

# LoliaFrpClient-fnOS

LoliaFrp 内网穿透服务的飞牛客户端

[![License](https://img.shields.io/github/license/Lolia-FRP/loliafrp-fnos-client)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Vue](https://img.shields.io/badge/Vue-3.5-4FC08D?logo=vuedotjs&logoColor=white)](https://vuejs.org/)
[![fnOS](https://img.shields.io/badge/fnOS-1.1.3100+-FF69B4)](https://www.fnnas.com/)

</div>

## 功能特性

- **总览与流量** - 查看账号信息、剩余流量配额与每日流量趋势图
- **隧道管理** - 搜索、筛选、创建、编辑与删除隧道
- **节点列表** - 查看节点在线状态、负载与计费倍率，创建隧道
- **域名管理** - 添加自定义域名并进行 DNS TXT 验证
- **Frpc 管理** - 自动下载、安装和更新适合本机的 frpc 核心
- **运行日志与自启** - 查看实时运行日志，支持应用启动时自动恢复隧道

## 技术栈

- **后端**: [Go](https://go.dev/) (原生 Unix Socket 服务，轻量高效)
- **前端**: [Vue 3](https://vuejs.org/) + [TypeScript](https://www.typescriptlang.org/) + [Element Plus](https://element-plus.org/)
- **平台**: [fnOS](https://www.fnnas.com/) (x86_64 / arm64)

## 快速开始

### 从源码构建

1. **克隆仓库**
   ```bash
   git clone https://github.com/Lolia-FRP/loliafrp-fnos-client.git
   cd loliafrp-fnos-client
   ```

2. **构建安装包 (.fpk)**
   ```bash
   # 构建 x86_64 (amd64) 安装包
   ./scripts/build-fnos.sh --arch amd64

   # 构建 arm64 安装包
   ./scripts/build-fnos.sh --arch arm64
   ```

### 发布版本

下载最新的发布版本见 [Releases](https://github.com/Lolia-FRP/loliafrp-fnos-client/releases) 页面。

## 使用说明

1. **安装应用** - 在飞牛应用中心手动安装 `.fpk` 文件
2. **登录账户** - 打开应用，通过浏览器授权登录您的 LoliaFrp 账户
3. **准备核心** - 登录后系统将自动下载适合本机的 frpc 核心
4. **启动隧道** - 创建隧道并点击启动，可在日志页查看运行状态

## 致谢

- [LoliaFRP](https://github.com/Lolia-FRP) - 内网穿透服务
- [fnOS](https://www.fnnas.com/) - 飞牛私有云操作系统

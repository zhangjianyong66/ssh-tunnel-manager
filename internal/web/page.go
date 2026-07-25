package web

const pageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>SSH 隧道管理器</title>
  <style>
    :root { color-scheme: light; font-family: system-ui, sans-serif; }
    * { box-sizing: border-box; }
    body { max-width: 1120px; margin: 36px auto; padding: 0 20px; color: #1f2937; }
    header { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; border-bottom: 1px solid #e5e7eb; margin-bottom: 22px; }
    .header-copy { min-width: 0; }
    .header-actions { padding-top: 16px; }
    h1 { margin-bottom: 8px; font-size: 28px; letter-spacing: 0; }
    h2 { margin: 0 0 10px; font-size: 19px; letter-spacing: 0; }
    .muted { color: #6b7280; }
    .toolbar, .port-toolbar, .actions { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
    .toolbar { margin: 18px 0; }
    button { min-height: 34px; border: 1px solid #cbd5e1; background: white; border-radius: 6px; padding: 7px 11px; cursor: pointer; white-space: nowrap; }
    button:hover { background: #f8fafc; }
    button:focus-visible { outline: 2px solid #0f766e; outline-offset: 2px; }
    button:disabled { cursor: wait; opacity: .6; }
    .danger { color: #b91c1c; border-color: #fecaca; }
    .table-scroll { max-width: 100%; overflow-x: auto; }
    table { width: 100%; border-collapse: collapse; }
    th, td { text-align: left; padding: 11px 8px; border-bottom: 1px solid #e5e7eb; vertical-align: top; }
    .hosts-table { min-width: 620px; }
    .tunnel-table { min-width: 760px; }
    .tunnels-section > .table-scroll > .tunnel-table { min-width: 980px; }
    .status, .port-number, .address { font-family: ui-monospace, monospace; }
    .address { color: #115e59; }
    .error { color: #b91c1c; white-space: pre-wrap; }
    .inline-error { display: block; max-width: 280px; margin-top: 4px; color: #b91c1c; font-size: 13px; }
    .port-row > td { padding: 0 8px 18px; }
    .port-panel { border-left: 3px solid #0f766e; padding: 10px 0 4px 16px; }
    .port-panel th, .port-panel td { padding: 8px 6px; }
    .port-toolbar { margin: 0 0 8px; }
    .port-toolbar strong { margin-right: auto; }
    .auto-refresh { display: inline-flex; align-items: center; gap: 6px; white-space: nowrap; }
    .tunnels-section { margin-top: 30px; padding-top: 20px; border-top: 1px solid #d1d5db; }
    .tunnel-status { font-family: ui-monospace, monospace; font-weight: 600; }
    .tunnel-status[data-status="running"] { color: #047857; }
    .tunnel-status[data-status="failed"] { color: #b91c1c; }
    .tunnel-status[data-status="starting"], .tunnel-status[data-status="stopping"], .tunnel-status[data-status="waiting_reconnect"], .tunnel-status[data-status="reconnecting"] { color: #a16207; }
    .runtime, .reconnect-count { white-space: nowrap; font-variant-numeric: tabular-nums; }
    .retry-time { display: block; margin-top: 4px; color: #6b7280; font-size: 13px; white-space: nowrap; }
    .log-row > td { padding: 0 8px 14px; background: #f8fafc; }
    .tunnel-log { border-left: 3px solid #64748b; padding: 8px 12px; }
    .log-entry { display: grid; grid-template-columns: 82px 64px minmax(180px, 1fr); gap: 10px; padding: 6px 0; border-bottom: 1px solid #e5e7eb; }
    .log-entry:last-child { border-bottom: 0; }
    .log-time, .log-level { color: #475569; font-family: ui-monospace, monospace; font-size: 12px; }
    .log-diagnostic { grid-column: 3; margin: 2px 0 0; color: #475569; font-family: ui-monospace, monospace; font-size: 12px; white-space: pre-wrap; overflow-wrap: anywhere; }
    dialog { border: 1px solid #cbd5e1; border-radius: 8px; padding: 20px; width: min(420px, calc(100vw - 40px)); }
    dialog::backdrop { background: rgb(15 23 42 / 35%); }
    dialog label { display: block; margin: 12px 0; }
    dialog input[type="text"], dialog input[type="password"] { display: block; width: 100%; margin-top: 5px; padding: 8px; border: 1px solid #cbd5e1; border-radius: 5px; }
    .dialog-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 18px; }
    @media (max-width: 640px) {
      body { margin-top: 20px; padding: 0 12px; }
      header { align-items: center; }
      .header-actions { padding-top: 0; }
      th, td { padding: 9px 6px; }
      .hosts-table { min-width: 100%; table-layout: fixed; }
      .hosts-table > thead th:nth-child(1) { width: 34%; }
      .hosts-table > thead th:nth-child(3) { width: 34%; }
      .hosts-table > thead th:nth-child(4) { width: 32%; }
      .source-column { display: none; }
      .port-row > td { width: 100%; max-width: 0; padding-left: 4px; padding-right: 4px; }
      .port-panel { padding-left: 9px; }
      .port-toolbar { align-items: flex-start; }
      .port-toolbar strong { width: 100%; }
      .actions { min-width: 148px; }
      .log-entry { grid-template-columns: 72px 58px minmax(150px, 1fr); gap: 6px; }
    }
  </style>
</head>
<body>
  <header>
    <div class="header-copy">
      <h1>SSH 隧道管理器</h1>
      <p class="muted">管理本机 OpenSSH 服务器连接、远程监听端口与本地转发。</p>
    </div>
    <div class="header-actions"><button type="button" id="shutdown" class="danger">退出程序</button></div>
  </header>
  <div class="toolbar">
    <button type="button" id="refresh">刷新 Host</button>
    <span id="message" class="muted" aria-live="polite"></span>
  </div>
  <div class="table-scroll">
    <table class="hosts-table">
      <thead><tr><th>Host</th><th class="source-column">来源</th><th>状态</th><th>操作</th></tr></thead>
      <tbody id="hosts"><tr><td colspan="4">加载中...</td></tr></tbody>
    </table>
  </div>
  <p id="error" class="error" role="alert"></p>
  <section class="tunnels-section" aria-labelledby="tunnels-title">
    <h2 id="tunnels-title">活动隧道</h2>
    <div class="table-scroll">
      <table class="tunnel-table">
        <thead><tr><th>Host</th><th>远程端口</th><th>本地映射</th><th>状态</th><th>运行时长</th><th>重连</th><th>操作</th></tr></thead>
        <tbody id="tunnels"><tr><td colspan="7">加载中...</td></tr></tbody>
      </table>
    </div>
  </section>
  <dialog id="credential-dialog">
    <form method="dialog">
      <h2>SSH 凭据</h2>
      <label>用户名（可选）<input id="credential-username" type="text" autocomplete="username"></label>
      <label>服务器密码（可选）<input id="credential-password" type="password" autocomplete="current-password"></label>
      <label>私钥口令（可选）<input id="credential-passphrase" type="password" autocomplete="off"></label>
      <label><input id="credential-save" type="checkbox"> 保存到系统密钥环</label>
      <div class="dialog-actions"><button value="cancel">取消</button><button value="connect">连接</button></div>
    </form>
  </dialog>
  <script>
    const hosts = document.getElementById('hosts');
    const tunnels = document.getElementById('tunnels');
    const message = document.getElementById('message');
    const error = document.getElementById('error');
    const credentialDialog = document.getElementById('credential-dialog');
    const busyTargets = new Set();
    const expandedLogs = new Set();
    const tunnelLogs = new Map();
    let currentTunnels = [];
    let loading = false;
    let reloadPending = false;

    function requestCredentials() {
      document.getElementById('credential-password').value = '';
      document.getElementById('credential-passphrase').value = '';
      document.getElementById('credential-save').checked = false;
      credentialDialog.returnValue = 'cancel';
      credentialDialog.showModal();
      return new Promise(resolve => credentialDialog.addEventListener('close', () => {
        if (credentialDialog.returnValue !== 'connect') return resolve(null);
        const password = document.getElementById('credential-password').value;
        const passphrase = document.getElementById('credential-passphrase').value;
        const save = document.getElementById('credential-save').checked;
        resolve({ username: document.getElementById('credential-username').value, password, passphrase, savePassword: save && !!password, savePassphrase: save && !!passphrase });
      }, { once: true }));
    }

    function showError(value) {
      error.textContent = value && value.message ? value.message : '操作失败';
    }

    function announce(value) {
      message.textContent = value;
    }

    function tunnelKey(host, remotePort) {
      return JSON.stringify([host, Number(remotePort)]);
    }

    function tunnelStatus(status) {
      return ({ starting: '启动中', running: '运行中', waiting_reconnect: '等待重连', reconnecting: '正在重连', stopping: '停止中', failed: '需要处理' })[status] || status || '未知';
    }

    function formatDuration(startedAt) {
      if (!startedAt) return '—';
      const seconds = Math.max(0, Math.floor((Date.now() - new Date(startedAt).getTime()) / 1000));
      const hours = Math.floor(seconds / 3600);
      const minutes = Math.floor((seconds % 3600) / 60);
      const rest = seconds % 60;
      if (hours) return hours + ' 小时 ' + minutes + ' 分';
      if (minutes) return minutes + ' 分 ' + rest + ' 秒';
      return rest + ' 秒';
    }

    function retryText(nextRetryAt) {
      if (!nextRetryAt) return '';
      const seconds = Math.max(0, Math.ceil((new Date(nextRetryAt).getTime() - Date.now()) / 1000));
      return seconds > 0 ? seconds + ' 秒后重试' : '即将重试';
    }

    function setBusy(key, active, label) {
      if (active) busyTargets.add(key); else busyTargets.delete(key);
      for (const button of document.querySelectorAll('button[data-busy-key]')) {
        if (button.dataset.busyKey !== key) continue;
        button.disabled = active;
        button.textContent = active ? label : button.dataset.idleLabel;
      }
    }

    function actionButton(label, key, handler, className) {
      const button = document.createElement('button');
      button.type = 'button';
      button.textContent = label;
      button.dataset.idleLabel = label;
      button.dataset.busyKey = key;
      button.disabled = busyTargets.has(key);
      if (className) button.className = className;
      button.onclick = handler;
      return button;
    }

    async function responseJSON(response) {
      try { return await response.json(); } catch (_) { return {}; }
    }

    async function createTunnel(host, remotePort) {
      const key = tunnelKey(host, remotePort);
      if (busyTargets.has(key)) return;
      error.textContent = '';
      setBusy(key, true, '代理中...');
      try {
        const response = await fetch('/api/tunnels', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ host, remotePort })
        });
        const result = await responseJSON(response);
        if (!response.ok) throw result;
        announce('已建立 ' + result.address);
      } catch (value) {
        showError(value);
      } finally {
        setBusy(key, false, '');
        await load();
      }
    }

    async function stopTunnel(item) {
      const key = tunnelKey(item.host, item.remotePort);
      if (busyTargets.has(key)) return;
      error.textContent = '';
      setBusy(key, true, item.status === 'failed' ? '清除中...' : '停止中...');
      try {
        const response = await fetch('/api/tunnels/' + encodeURIComponent(item.id), { method: 'DELETE' });
        if (!response.ok) throw await responseJSON(response);
        announce(item.status === 'failed' ? '已清除失败记录' : '已停止隧道');
      } catch (value) {
        showError(value);
      } finally {
        setBusy(key, false, '');
        await load();
      }
    }

    async function copyAddress(address) {
      try {
        await navigator.clipboard.writeText(address);
        announce('已复制 ' + address);
      } catch (_) {
        showError({ message: '复制失败，请手动选择本地地址' });
      }
    }

    function openAddress(address) {
      window.open('http://' + address + '/', '_blank', 'noopener,noreferrer');
    }

    async function loadTunnelLogs(id) {
      try {
        const response = await fetch('/api/tunnels/' + encodeURIComponent(id) + '/logs');
        const result = await responseJSON(response);
        if (!response.ok) throw result;
        tunnelLogs.set(id, { logs: result.logs || [] });
      } catch (value) {
        tunnelLogs.set(id, { error: value && value.message ? value.message : '日志加载失败', logs: [] });
      }
    }

    async function toggleTunnelLogs(item) {
      if (expandedLogs.has(item.id)) {
        expandedLogs.delete(item.id);
      } else {
        expandedLogs.add(item.id);
        await loadTunnelLogs(item.id);
      }
      renderTunnels(currentTunnels);
    }

    function logPanel(item) {
      const panel = document.createElement('div');
      panel.className = 'tunnel-log';
      const state = tunnelLogs.get(item.id) || { logs: [] };
      if (state.error) {
        const notice = document.createElement('p');
        notice.className = 'error';
        notice.textContent = state.error;
        panel.appendChild(notice);
      }
      for (const entry of state.logs) {
        const row = document.createElement('div');
        row.className = 'log-entry';
        const time = document.createElement('time');
        time.className = 'log-time';
        time.dateTime = entry.time || '';
        time.textContent = entry.time ? new Date(entry.time).toLocaleTimeString() : '—';
        const level = document.createElement('span');
        level.className = 'log-level';
        level.textContent = ({ info: '信息', warning: '警告', error: '错误' })[entry.level] || entry.level || '信息';
        const content = document.createElement('span');
        content.textContent = entry.message || '';
        row.append(time, level, content);
        if (entry.diagnostic) {
          const diagnostic = document.createElement('pre');
          diagnostic.className = 'log-diagnostic';
          diagnostic.textContent = entry.diagnostic;
          row.appendChild(diagnostic);
        }
        panel.appendChild(row);
      }
      if (!state.error && state.logs.length === 0) {
        const empty = document.createElement('span');
        empty.className = 'muted';
        empty.textContent = '暂无日志';
        panel.appendChild(empty);
      }
      return panel;
    }

    function tunnelActions(item, showLogs = true) {
      const actions = document.createElement('div');
      actions.className = 'actions';
      const key = tunnelKey(item.host, item.remotePort);
      if (item.status === 'running') {
        const copy = document.createElement('button');
        copy.type = 'button';
        copy.textContent = '复制';
        copy.onclick = () => copyAddress(item.address);
        const open = document.createElement('button');
        open.type = 'button';
        open.textContent = '打开';
        open.onclick = () => openAddress(item.address);
        actions.append(copy, open, actionButton('停止', key, () => stopTunnel(item), 'danger'));
      } else if (item.status === 'failed') {
        actions.append(
          actionButton('重新代理', key, () => createTunnel(item.host, item.remotePort)),
          actionButton('清除', key, () => stopTunnel(item), 'danger')
        );
      } else {
        const pending = document.createElement('button');
        pending.type = 'button';
        pending.textContent = tunnelStatus(item.status);
        pending.disabled = true;
        actions.appendChild(pending);
        if (item.status !== 'stopping') actions.appendChild(actionButton('停止', key, () => stopTunnel(item), 'danger'));
      }
      if (showLogs) {
        const logs = document.createElement('button');
        logs.type = 'button';
        logs.textContent = expandedLogs.has(item.id) ? '收起日志' : '日志';
        logs.setAttribute('aria-expanded', expandedLogs.has(item.id) ? 'true' : 'false');
        logs.onclick = () => toggleTunnelLogs(item);
        actions.appendChild(logs);
      }
      return actions;
    }

    function mappingCell(item) {
      const cell = document.createElement('td');
      if (item && item.address) {
        const address = document.createElement('span');
        address.className = 'address';
        address.textContent = item.address;
        cell.appendChild(address);
      } else {
        cell.textContent = '—';
        cell.className = 'muted';
      }
      return cell;
    }

    function statusCell(item, emptyText) {
      const cell = document.createElement('td');
      if (!item) {
        cell.className = 'muted';
        cell.textContent = emptyText;
        return cell;
      }
      const status = document.createElement('span');
      status.className = 'tunnel-status';
      status.dataset.status = item.status;
      status.textContent = tunnelStatus(item.status);
      cell.appendChild(status);
      if (item.status === 'waiting_reconnect' && item.nextRetryAt) {
        const retry = document.createElement('span');
        retry.className = 'retry-time';
        retry.textContent = retryText(item.nextRetryAt);
        cell.appendChild(retry);
      }
      if (item.lastError && item.lastError.message) {
        const detail = document.createElement('span');
        detail.className = 'inline-error';
        detail.textContent = item.lastError.message;
        cell.appendChild(detail);
      }
      return cell;
    }

    function renderTunnels(items) {
      currentTunnels = items;
      tunnels.innerHTML = '';
      for (const item of items) {
        const row = document.createElement('tr');
        const host = document.createElement('td');
        host.textContent = item.host;
        const remote = document.createElement('td');
        remote.className = 'port-number';
        remote.textContent = String(item.remotePort);
        const actions = document.createElement('td');
        actions.appendChild(tunnelActions(item));
        const runtime = document.createElement('td');
        runtime.className = 'runtime';
        runtime.textContent = item.status === 'running' ? formatDuration(item.runningSince) : '—';
        const reconnects = document.createElement('td');
        reconnects.className = 'reconnect-count';
        reconnects.textContent = String(item.reconnectCount || 0);
        row.append(host, remote, mappingCell(item), statusCell(item, ''), runtime, reconnects, actions);
        tunnels.appendChild(row);
        if (expandedLogs.has(item.id)) {
          const logRow = document.createElement('tr');
          logRow.className = 'log-row';
          const logCell = document.createElement('td');
          logCell.colSpan = 7;
          logCell.appendChild(logPanel(item));
          logRow.appendChild(logCell);
          tunnels.appendChild(logRow);
        }
      }
      if (items.length === 0) {
        const row = document.createElement('tr');
        const empty = document.createElement('td');
        empty.colSpan = 7;
        empty.className = 'muted';
        empty.textContent = '暂无隧道';
        row.appendChild(empty);
        tunnels.appendChild(row);
      }
    }

    function portPanel(host, state, tunnelByTarget) {
      const panel = document.createElement('div');
      panel.className = 'port-panel';
      const toolbar = document.createElement('div');
      toolbar.className = 'port-toolbar';
      const title = document.createElement('strong');
      title.textContent = '监听端口';
      const meta = document.createElement('span');
      meta.className = 'muted';
      meta.textContent = state.refreshedAt ? '更新于 ' + new Date(state.refreshedAt).toLocaleTimeString() : '尚未探测';
      const refresh = document.createElement('button');
      refresh.type = 'button';
      refresh.textContent = state.refreshing ? '刷新中...' : '刷新端口';
      refresh.disabled = !!state.refreshing;
      refresh.onclick = async () => {
        error.textContent = '';
        refresh.disabled = true;
        try {
          const response = await fetch('/api/servers/' + encodeURIComponent(host) + '/ports/refresh', { method: 'POST' });
          const result = await responseJSON(response);
          if (!response.ok) showError(result);
        } catch (value) {
          showError(value);
        }
        await load();
      };
      const autoLabel = document.createElement('label');
      autoLabel.className = 'auto-refresh';
      const auto = document.createElement('input');
      auto.type = 'checkbox';
      auto.checked = !!state.autoRefresh;
      auto.onchange = async () => {
        error.textContent = '';
        auto.disabled = true;
        try {
          const response = await fetch('/api/servers/' + encodeURIComponent(host) + '/ports/auto-refresh', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ enabled: auto.checked })
          });
          const result = await responseJSON(response);
          if (!response.ok) showError(result);
        } catch (value) {
          showError(value);
        }
        await load();
      };
      autoLabel.append(auto, document.createTextNode('每 10 秒自动刷新'));
      toolbar.append(title, meta, refresh, autoLabel);
      panel.appendChild(toolbar);
      if (state.lastError) {
        const notice = document.createElement('p');
        notice.className = 'error';
        notice.textContent = state.lastError.message;
        panel.appendChild(notice);
      }
      const scroll = document.createElement('div');
      scroll.className = 'table-scroll';
      const table = document.createElement('table');
      table.className = 'tunnel-table';
      table.innerHTML = '<thead><tr><th>远程端口</th><th>进程</th><th>本地映射</th><th>状态</th><th>操作</th></tr></thead>';
      const body = document.createElement('tbody');
      for (const port of state.ports || []) {
        const item = tunnelByTarget.get(tunnelKey(host, port.number));
        const row = document.createElement('tr');
        const number = document.createElement('td');
        number.className = 'port-number';
        number.textContent = String(port.number);
        const process = document.createElement('td');
        process.textContent = port.process || '不可见';
        const actions = document.createElement('td');
        if (item) {
          actions.appendChild(tunnelActions(item, false));
        } else {
          const key = tunnelKey(host, port.number);
          actions.appendChild(actionButton('代理', key, () => createTunnel(host, port.number)));
        }
        row.append(number, process, mappingCell(item), statusCell(item, '未代理'), actions);
        body.appendChild(row);
      }
      if (!state.ports || state.ports.length === 0) {
        const row = document.createElement('tr');
        const empty = document.createElement('td');
        empty.colSpan = 5;
        empty.className = 'muted';
        empty.textContent = state.refreshedAt ? '未发现 TCP 监听端口' : '点击刷新端口开始探测';
        row.appendChild(empty);
        body.appendChild(row);
      }
      table.appendChild(body);
      scroll.appendChild(table);
      panel.appendChild(scroll);
      return panel;
    }

    async function loadOnce() {
      const [hostsResponse, tunnelResponse] = await Promise.all([
        fetch('/api/ssh-hosts'),
        fetch('/api/tunnels')
      ]);
      const [hostData, tunnelData] = await Promise.all([
        responseJSON(hostsResponse),
        responseJSON(tunnelResponse)
      ]);
      if (!hostsResponse.ok) throw hostData;
      if (!tunnelResponse.ok) throw tunnelData;
      const tunnelItems = tunnelData.tunnels || [];
      const tunnelIDs = new Set(tunnelItems.map(item => item.id));
      for (const id of [...expandedLogs]) {
        if (!tunnelIDs.has(id)) {
          expandedLogs.delete(id);
          tunnelLogs.delete(id);
        }
      }
      await Promise.all([...expandedLogs].map(id => loadTunnelLogs(id)));
      const tunnelByTarget = new Map(tunnelItems.map(item => [tunnelKey(item.host, item.remotePort), item]));
      renderTunnels(tunnelItems);
      hosts.innerHTML = '';
      for (const host of hostData.hosts || []) {
        const row = document.createElement('tr');
        row.innerHTML = '<td></td><td class="source-column"></td><td class="status">加载中...</td><td></td>';
        row.children[0].textContent = host.alias;
        row.children[1].textContent = host.source;
        const action = document.createElement('button');
        action.type = 'button';
        action.textContent = '连接';
        row.children[3].appendChild(action);
        const stateResponse = await fetch('/api/servers/' + encodeURIComponent(host.alias));
        const state = await responseJSON(stateResponse);
        if (!stateResponse.ok) throw state;
        row.children[2].textContent = state.status;
        action.textContent = state.status === 'connected' ? '断开' : '连接';
        action.onclick = async () => {
          error.textContent = '';
          action.disabled = true;
          const endpoint = state.status === 'connected' ? 'disconnect' : 'connect';
          try {
            let operation = await fetch('/api/servers/' + encodeURIComponent(host.alias) + '/' + endpoint, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: endpoint === 'connect' ? '{}' : undefined
            });
            let result = await responseJSON(operation);
            if (endpoint === 'connect' && !operation.ok && result.code === 'credential_required') {
              const credentials = await requestCredentials();
              if (!credentials) { action.disabled = false; return; }
              operation = await fetch('/api/servers/' + encodeURIComponent(host.alias) + '/connect', {
                method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(credentials)
              });
              result = await responseJSON(operation);
            }
            if (!operation.ok) {
              showError(result);
            } else if (endpoint === 'connect') {
              const discovery = await fetch('/api/servers/' + encodeURIComponent(host.alias) + '/ports/refresh', { method: 'POST' });
              if (!discovery.ok) showError(await responseJSON(discovery));
            }
          } catch (value) {
            showError(value);
          }
          await load();
        };
        hosts.appendChild(row);
        if (state.status === 'connected') {
          const portResponse = await fetch('/api/servers/' + encodeURIComponent(host.alias) + '/ports');
          const portState = await responseJSON(portResponse);
          if (portResponse.ok) {
            const portRow = document.createElement('tr');
            portRow.className = 'port-row';
            const cell = document.createElement('td');
            cell.colSpan = 4;
            cell.appendChild(portPanel(host.alias, portState, tunnelByTarget));
            portRow.appendChild(cell);
            hosts.appendChild(portRow);
          }
        }
      }
      if (!hostData.hosts || hostData.hosts.length === 0) hosts.innerHTML = '<tr><td colspan="4">未找到显式 SSH Host</td></tr>';
      announce('已刷新');
    }

    async function load() {
      if (loading) {
        reloadPending = true;
        return;
      }
      loading = true;
      try {
        do {
          reloadPending = false;
          await loadOnce();
        } while (reloadPending);
      } catch (value) {
        showError(value);
      } finally {
        loading = false;
      }
    }

    document.getElementById('refresh').onclick = async () => {
      error.textContent = '';
      const response = await fetch('/api/ssh-hosts/refresh', { method: 'POST' });
      if (!response.ok) showError(await responseJSON(response));
      await load();
    };
    document.getElementById('shutdown').onclick = async event => {
      if (!window.confirm('退出程序将停止所有 SSH 隧道，是否继续？')) return;
      const button = event.currentTarget;
      button.disabled = true;
      button.textContent = '退出中...';
      error.textContent = '';
      try {
        const response = await fetch('/api/shutdown', { method: 'POST' });
        if (!response.ok) throw await responseJSON(response);
        announce('程序正在退出');
      } catch (value) {
        button.disabled = false;
        button.textContent = '退出程序';
        showError(value);
      }
    };
    load();
    setInterval(load, 10000);
  </script>
</body>
</html>`

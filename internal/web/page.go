package web

const pageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>SSH 隧道管理器</title>
  <style>
    :root { color-scheme: light; font-family: system-ui, sans-serif; }
    body { max-width: 980px; margin: 36px auto; padding: 0 20px; color: #1f2937; }
    header { border-bottom: 1px solid #e5e7eb; margin-bottom: 22px; }
    h1 { margin-bottom: 8px; font-size: 28px; letter-spacing: 0; }
    .muted { color: #6b7280; }
    .toolbar, .port-toolbar { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; margin: 18px 0; }
    button { border: 1px solid #cbd5e1; background: white; border-radius: 6px; padding: 7px 12px; cursor: pointer; }
    button:hover { background: #f8fafc; }
    button:disabled { cursor: wait; opacity: .6; }
    table { width: 100%; border-collapse: collapse; }
    th, td { text-align: left; padding: 11px 8px; border-bottom: 1px solid #e5e7eb; }
    .status, .port-number { font-family: ui-monospace, monospace; }
    .error { color: #b91c1c; white-space: pre-wrap; }
    .port-row > td { padding: 0 8px 18px; }
    .port-panel { border-left: 3px solid #0f766e; padding: 10px 0 4px 16px; }
    .port-panel table { max-width: 620px; }
    .port-panel th, .port-panel td { padding: 7px 6px; }
    .port-toolbar { margin: 0 0 8px; }
    .port-toolbar strong { margin-right: auto; }
    .auto-refresh { display: inline-flex; align-items: center; gap: 6px; }
    dialog { border: 1px solid #cbd5e1; border-radius: 8px; padding: 20px; width: min(420px, calc(100vw - 40px)); }
    dialog::backdrop { background: rgb(15 23 42 / 35%); }
    dialog label { display: block; margin: 12px 0; }
    dialog input[type="text"], dialog input[type="password"] { display: block; box-sizing: border-box; width: 100%; margin-top: 5px; padding: 8px; border: 1px solid #cbd5e1; border-radius: 5px; }
    .dialog-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 18px; }
    @media (max-width: 640px) {
      body { margin-top: 20px; padding: 0 12px; }
      th, td { padding: 9px 5px; }
      .source-column { display: none; }
      .port-panel { padding-left: 10px; }
    }
  </style>
</head>
<body>
  <header>
    <h1>SSH 隧道管理器</h1>
    <p class="muted">管理本机 OpenSSH 服务器连接与远程监听端口。</p>
  </header>
  <div class="toolbar">
    <button type="button" id="refresh">刷新 Host</button>
    <span id="message" class="muted"></span>
  </div>
  <table>
    <thead><tr><th>Host</th><th class="source-column">来源</th><th>状态</th><th>操作</th></tr></thead>
    <tbody id="hosts"><tr><td colspan="4">加载中...</td></tr></tbody>
  </table>
  <p id="error" class="error"></p>
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
    const message = document.getElementById('message');
    const error = document.getElementById('error');
    const credentialDialog = document.getElementById('credential-dialog');
    let loading = false;

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

    function portPanel(host, state) {
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
        const response = await fetch('/api/servers/' + encodeURIComponent(host) + '/ports/refresh', { method: 'POST' });
        const result = await response.json();
        if (!response.ok) showError(result);
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
        const response = await fetch('/api/servers/' + encodeURIComponent(host) + '/ports/auto-refresh', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ enabled: auto.checked })
        });
        const result = await response.json();
        if (!response.ok) showError(result);
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
      const table = document.createElement('table');
      table.innerHTML = '<thead><tr><th>远程端口</th><th>进程</th></tr></thead>';
      const body = document.createElement('tbody');
      for (const port of state.ports || []) {
        const row = document.createElement('tr');
        const number = document.createElement('td');
        number.className = 'port-number';
        number.textContent = String(port.number);
        const process = document.createElement('td');
        process.textContent = port.process || '不可见';
        row.append(number, process);
        body.appendChild(row);
      }
      if (!state.ports || state.ports.length === 0) {
        const row = document.createElement('tr');
        const empty = document.createElement('td');
        empty.colSpan = 2;
        empty.className = 'muted';
        empty.textContent = state.refreshedAt ? '未发现 TCP 监听端口' : '点击刷新端口开始探测';
        row.appendChild(empty);
        body.appendChild(row);
      }
      table.appendChild(body);
      panel.appendChild(table);
      return panel;
    }

    async function load() {
      if (loading) return;
      loading = true;
      try {
        const response = await fetch('/api/ssh-hosts');
        const data = await response.json();
        if (!response.ok) throw data;
        hosts.innerHTML = '';
        for (const host of data.hosts || []) {
          const row = document.createElement('tr');
          row.innerHTML = '<td></td><td class="source-column"></td><td class="status">加载中...</td><td></td>';
          row.children[0].textContent = host.alias;
          row.children[1].textContent = host.source;
          const action = document.createElement('button');
          action.type = 'button';
          action.textContent = '连接';
          row.children[3].appendChild(action);
          const stateResponse = await fetch('/api/servers/' + encodeURIComponent(host.alias));
          const state = await stateResponse.json();
          row.children[2].textContent = state.status;
          action.textContent = state.status === 'connected' ? '断开' : '连接';
          action.onclick = async () => {
            error.textContent = '';
            action.disabled = true;
            const endpoint = state.status === 'connected' ? 'disconnect' : 'connect';
            let operation = await fetch('/api/servers/' + encodeURIComponent(host.alias) + '/' + endpoint, {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: endpoint === 'connect' ? '{}' : undefined
            });
            let result = await operation.json();
            if (endpoint === 'connect' && !operation.ok && result.code === 'credential_required') {
              const credentials = await requestCredentials();
              if (!credentials) { action.disabled = false; return; }
              operation = await fetch('/api/servers/' + encodeURIComponent(host.alias) + '/connect', {
                method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(credentials)
              });
              result = await operation.json();
            }
            if (!operation.ok) {
              showError(result);
            } else if (endpoint === 'connect') {
              const discovery = await fetch('/api/servers/' + encodeURIComponent(host.alias) + '/ports/refresh', { method: 'POST' });
              if (!discovery.ok) showError(await discovery.json());
            }
            await load();
          };
          hosts.appendChild(row);
          if (state.status === 'connected') {
            const portResponse = await fetch('/api/servers/' + encodeURIComponent(host.alias) + '/ports');
            const portState = await portResponse.json();
            if (portResponse.ok) {
              const portRow = document.createElement('tr');
              portRow.className = 'port-row';
              const cell = document.createElement('td');
              cell.colSpan = 4;
              cell.appendChild(portPanel(host.alias, portState));
              portRow.appendChild(cell);
              hosts.appendChild(portRow);
            }
          }
        }
        if (!data.hosts || data.hosts.length === 0) hosts.innerHTML = '<tr><td colspan="4">未找到显式 SSH Host</td></tr>';
        message.textContent = '已刷新';
      } catch (value) {
        showError(value);
      } finally {
        loading = false;
      }
    }

    document.getElementById('refresh').onclick = async () => {
      error.textContent = '';
      const response = await fetch('/api/ssh-hosts/refresh', { method: 'POST' });
      if (!response.ok) showError(await response.json());
      await load();
    };
    load();
    setInterval(load, 10000);
  </script>
</body>
</html>`

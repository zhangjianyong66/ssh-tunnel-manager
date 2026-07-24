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
    h1 { margin-bottom: 8px; }
    .muted { color: #6b7280; }
    .toolbar { display: flex; gap: 8px; align-items: center; margin: 18px 0; }
    button { border: 1px solid #cbd5e1; background: white; border-radius: 6px; padding: 7px 12px; cursor: pointer; }
    button:hover { background: #f8fafc; }
    table { width: 100%; border-collapse: collapse; }
    th, td { text-align: left; padding: 11px 8px; border-bottom: 1px solid #e5e7eb; }
    .status { font-family: ui-monospace, monospace; }
    .error { color: #b91c1c; white-space: pre-wrap; }
    dialog { border: 1px solid #cbd5e1; border-radius: 8px; padding: 20px; width: min(420px, calc(100vw - 40px)); }
    dialog::backdrop { background: rgb(15 23 42 / 35%); }
    dialog label { display: block; margin: 12px 0; }
    dialog input[type="text"], dialog input[type="password"] { display: block; box-sizing: border-box; width: 100%; margin-top: 5px; padding: 8px; border: 1px solid #cbd5e1; border-radius: 5px; }
    .dialog-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 18px; }
  </style>
</head>
<body>
  <header>
    <h1>SSH 隧道管理器</h1>
    <p class="muted">管理本机 OpenSSH 配置中的服务器连接。</p>
  </header>
  <div class="toolbar">
    <button type="button" id="refresh">刷新 Host</button>
    <span id="message" class="muted"></span>
  </div>
  <table>
    <thead><tr><th>Host</th><th>来源</th><th>状态</th><th>操作</th></tr></thead>
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
    async function load() {
      error.textContent = '';
      const response = await fetch('/api/ssh-hosts');
      const data = await response.json();
      hosts.innerHTML = '';
      for (const host of data.hosts || []) {
        const row = document.createElement('tr');
        row.innerHTML = '<td></td><td></td><td class="status">加载中...</td><td></td>';
        row.children[0].textContent = host.alias;
        row.children[1].textContent = host.source;
        const action = document.createElement('button');
        action.type = 'button';
        action.textContent = '连接';
        action.onclick = async () => {
          action.disabled = true;
          const current = await fetch('/api/servers/' + encodeURIComponent(host.alias));
          const state = await current.json();
          const endpoint = state.status === 'connected' ? 'disconnect' : 'connect';
          let response = await fetch('/api/servers/' + encodeURIComponent(host.alias) + '/' + endpoint, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: endpoint === 'connect' ? '{}' : undefined });
          let result = await response.json();
          if (endpoint === 'connect' && !response.ok && result.code === 'credential_required') {
            const credentials = await requestCredentials();
            if (!credentials) { action.disabled = false; return; }
            response = await fetch('/api/servers/' + encodeURIComponent(host.alias) + '/connect', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(credentials) });
            result = await response.json();
          }
          if (!response.ok) error.textContent = result.message || '操作失败';
          await load();
        };
        row.children[3].appendChild(action);
        const state = await fetch('/api/servers/' + encodeURIComponent(host.alias)).then(r => r.json());
        row.children[2].textContent = state.status;
        action.textContent = state.status === 'connected' ? '断开' : '连接';
        hosts.appendChild(row);
      }
      if (!data.hosts || data.hosts.length === 0) hosts.innerHTML = '<tr><td colspan="4">未找到显式 SSH Host</td></tr>';
      message.textContent = '已刷新';
    }
    document.getElementById('refresh').onclick = async () => { await fetch('/api/ssh-hosts/refresh', { method: 'POST' }); await load(); };
    load().catch(() => { error.textContent = '加载 SSH Host 失败'; });
  </script>
</body>
</html>`

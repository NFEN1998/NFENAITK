package server

import (
	"crypto/rand"
	"encoding/hex"
)

// newID returns a short random identifier for request correlation.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// queryTestPageHTML is served at "/" so operators can verify connectivity.
const queryTestPageHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>nfentik-go 服务</title>
<style>
  body{font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;max-width:720px;margin:48px auto;padding:0 20px;color:#0f172a;background:#f8fafc;}
  h1{font-size:24px;}
  .card{background:#fff;border:1px solid #e2e8f0;border-radius:12px;padding:20px;margin-top:16px;box-shadow:0 1px 2px rgba(15,23,42,.04);}
  label{display:block;font-size:13px;font-weight:600;margin:12px 0 4px;}
  input,textarea{width:100%;box-sizing:border-box;padding:8px 10px;border:1px solid #cbd5e1;border-radius:8px;font:inherit;}
  button{margin-top:16px;padding:9px 16px;border:none;border-radius:8px;background:#2563eb;color:#fff;font-weight:600;cursor:pointer;}
  pre{background:#0f172a;color:#e2e8f0;padding:14px;border-radius:8px;overflow:auto;font-size:13px;}
  a{color:#2563eb;}
</style>
</head>
<body>
<h1>nfentik-go 服务</h1>
<p>本服务提供 OCS 题库查询接口。把下列地址填入 OCS 用户脚本即可。</p>
<div class="card">
  <strong>接口地址</strong>
  <pre id="endpoint"></pre>
  <p>状态接口: <a href="/api/status">/api/status</a> · 管理控制台: <a href="/console">/console</a></p>
</div>
<div class="card">
  <strong>在线测试查询</strong>
  <label for="title">题目</label>
  <input id="title" placeholder="在此粘贴题目内容">
  <label for="options">选项（可选）</label>
  <textarea id="options" rows="3" placeholder="A. ... B. ..."></textarea>
  <label style="display:inline-flex;align-items:center;gap:6px;font-weight:400;margin-top:10px;">
    <input type="checkbox" id="raw" style="width:auto;"> 返回 HTML（含待修正按钮，等价于 raw=1）
  </label>
  <button onclick="runQuery()">查询</button>
  <pre id="result">等待查询...</pre>
</div>
<script>
document.getElementById('endpoint').textContent = location.origin + '/query';
async function runQuery(){
  const result = document.getElementById('result');
  result.textContent = '查询中...';
  try{
    const res = await fetch('/query',{method:'POST',headers:{'Content-Type':'application/json'},
      body:JSON.stringify({title:document.getElementById('title').value,options:document.getElementById('options').value||null,type:'single_choice',raw:document.getElementById('raw').checked})});
    const data = await res.json();
    result.textContent = JSON.stringify(data,null,2);
  }catch(err){ result.textContent = '请求失败: ' + err.message; }
}
</script>
</body>
</html>`

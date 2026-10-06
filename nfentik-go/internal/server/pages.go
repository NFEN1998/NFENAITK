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

// homePageHTML is the public landing page served at "/". It introduces the
// service and links to the console and user portal; it does not expose the
// query test form that operators previously used for connectivity checks.
const homePageHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>nfentik · 智能题库查询服务</title>
<style>
  :root{--brand:#2563eb;--ink:#0f172a;--muted:#64748b;--line:#e2e8f0;--bg:#f8fafc;}
  *{box-sizing:border-box;}
  body{margin:0;font-family:system-ui,-apple-system,"Segoe UI",Roboto,"PingFang SC","Microsoft YaHei",sans-serif;color:var(--ink);background:var(--bg);line-height:1.6;}
  a{color:var(--brand);text-decoration:none;}
  a:hover{text-decoration:underline;}
  .wrap{max-width:1080px;margin:0 auto;padding:0 24px;}
  header.nav{position:sticky;top:0;background:rgba(248,250,252,.85);backdrop-filter:blur(8px);border-bottom:1px solid var(--line);z-index:10;}
  .nav-inner{display:flex;align-items:center;justify-content:space-between;height:64px;}
  .brand{display:flex;align-items:center;gap:10px;}
  .logo{width:34px;height:34px;border-radius:9px;background:var(--brand);color:#fff;display:grid;place-items:center;font-weight:800;font-size:18px;}
  .brand strong{font-size:17px;}
  .brand small{display:block;font-size:11px;color:var(--muted);font-weight:500;}
  .nav a.pill-btn{padding:8px 16px;border-radius:999px;font-size:14px;font-weight:600;margin-left:10px;}
  .nav a.primary{background:var(--brand);color:#fff;}
  .nav a.primary:hover{text-decoration:none;opacity:.92;}
  .nav a.ghost{border:1px solid var(--line);color:var(--ink);background:#fff;}
  .nav a.ghost:hover{text-decoration:none;border-color:#cbd5e1;}
  .hero{padding:72px 0 56px;text-align:center;}
  .hero .tag{display:inline-block;font-size:13px;font-weight:600;color:var(--brand);background:#eff6ff;border:1px solid #dbeafe;border-radius:999px;padding:5px 14px;}
  .hero h1{font-size:44px;line-height:1.2;margin:20px 0 14px;letter-spacing:-.5px;}
  .hero p{font-size:17px;color:var(--muted);max-width:640px;margin:0 auto 28px;}
  .hero-actions{display:flex;gap:12px;justify-content:center;flex-wrap:wrap;}
  .btn{padding:11px 22px;border-radius:10px;font-weight:600;font-size:15px;}
  .btn.primary{background:var(--brand);color:#fff;}
  .btn.primary:hover{text-decoration:none;opacity:.92;}
  .btn.outline{border:1px solid var(--line);background:#fff;color:var(--ink);}
  .btn.outline:hover{text-decoration:none;border-color:#cbd5e1;}
  .features{display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:18px;padding:16px 0 8px;}
  .feature{background:#fff;border:1px solid var(--line);border-radius:14px;padding:22px;}
  .feature .ico{width:40px;height:40px;border-radius:10px;background:#eff6ff;color:var(--brand);display:grid;place-items:center;font-size:20px;margin-bottom:12px;}
  .feature h3{margin:0 0 6px;font-size:16px;}
  .feature p{margin:0;font-size:14px;color:var(--muted);}
  .steps{padding:56px 0;}
  .steps h2{text-align:center;font-size:28px;margin:0 0 8px;}
  .steps .sub{text-align:center;color:var(--muted);margin:0 0 34px;}
  .step-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:18px;}
  .step{background:#fff;border:1px solid var(--line);border-radius:14px;padding:22px;position:relative;}
  .step .num{width:28px;height:28px;border-radius:8px;background:var(--brand);color:#fff;display:grid;place-items:center;font-weight:700;font-size:14px;margin-bottom:12px;}
  .step h3{margin:0 0 6px;font-size:16px;}
  .step p{margin:0;font-size:14px;color:var(--muted);}
  .endpoint{background:#0f172a;color:#e2e8f0;border-radius:12px;padding:18px 20px;margin-top:30px;display:flex;align-items:center;justify-content:space-between;gap:14px;flex-wrap:wrap;}
  .endpoint .label{font-size:12px;color:#94a3b8;text-transform:uppercase;letter-spacing:.5px;}
  .endpoint code{font-size:15px;color:#7dd3fc;word-break:break-all;}
  .endpoint button{background:#1e293b;color:#e2e8f0;border:1px solid #334155;border-radius:8px;padding:8px 14px;font-weight:600;cursor:pointer;}
  .endpoint button:hover{background:#334155;}
  footer{border-top:1px solid var(--line);padding:28px 0;color:var(--muted);font-size:13px;text-align:center;}
  @media(max-width:640px){
    .hero h1{font-size:32px;}
    .nav a.pill-btn{margin-left:6px;padding:7px 12px;}
    .brand small{display:none;}
  }
</style>
</head>
<body>
<header class="nav">
  <div class="wrap nav-inner">
    <div class="brand">
      <span class="logo">Z</span>
      <div><strong>nfentik</strong><small>智能题库查询服务</small></div>
    </div>
    <nav>
      <a class="pill-btn primary" href="/user">用户中心</a>
    </nav>
  </div>
</header>

<section class="hero wrap">
  <span class="tag">OCS 题库 · 多用户 · 套餐计费</span>
  <h1>让答题查询快而准</h1>
  <p>nfentik 提供稳定高效的题库查询接口，支持本地题库精确匹配、Redis 缓存加速与 AI 智能兜底，助力团队协作答题。</p>
  <div class="hero-actions">
    <a class="btn primary" href="/user">进入用户中心</a>
  </div>
</section>

<section class="wrap features">
  <div class="feature">
    <div class="ico">⚡</div>
    <h3>极速查询</h3>
    <p>题库哈希索引精确命中，Redis 缓存二次加速，海量题目也能毫秒响应。</p>
  </div>
  <div class="feature">
    <div class="ico">🧠</div>
    <h3>AI 智能兜底</h3>
    <p>题库未命中时自动调用大模型作答，结果回写题库，越用越准。</p>
  </div>
  <div class="feature">
    <div class="ico">👥</div>
    <h3>多用户与套餐</h3>
    <p>令牌即身份，支持时长套餐与次数套餐，用量与调用记录一目了然。</p>
  </div>
  <div class="feature">
    <div class="ico">🔒</div>
    <h3>安全可控</h3>
    <p>管理令牌保护控制台，用户令牌独立鉴权，配额实时扣减、日志可追溯。</p>
  </div>
</section>

<section class="steps wrap">
  <h2>三步接入</h2>
  <p class="sub">在 OCS 用户脚本中配置查询地址即可使用</p>
  <div class="step-grid">
    <div class="step">
      <div class="num">1</div>
      <h3>获取用户令牌</h3>
      <p>由管理员在控制台创建用户并分配令牌，或进入用户中心领取。</p>
    </div>
    <div class="step">
      <div class="num">2</div>
      <h3>配置查询地址</h3>
      <p>将下方接口地址填入 OCS 用户脚本，并在请求头中携带用户令牌。</p>
    </div>
    <div class="step">
      <div class="num">3</div>
      <h3>开始答题</h3>
      <p>发送题目即可获得答案，命中题库或缓存同样计入套餐用量。</p>
    </div>
  </div>
  <div class="endpoint">
    <div>
      <div class="label">查询接口地址</div>
      <code id="endpoint"></code>
    </div>
    <button onclick="copyEndpoint()" id="copy-btn">复制</button>
  </div>
</section>

<footer>
  <div class="wrap">nfentik 智能题库查询服务</div>
</footer>

<script>
function copyEndpoint(){
  const text = document.getElementById('endpoint').textContent;
  navigator.clipboard?.writeText(text).then(function(){
    const btn = document.getElementById('copy-btn');
    const old = btn.textContent;
    btn.textContent = '已复制';
    setTimeout(function(){ btn.textContent = old; }, 1500);
  });
}
document.getElementById('endpoint').textContent = location.origin + '/query';
</script>
</body>
</html>`

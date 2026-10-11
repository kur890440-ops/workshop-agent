package webchat

import (
	"encoding/json"
	"errors"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"
)

const sessionCookie = "wa_session"
const pendingCookie = "wa_pending"

func (s *Service) cookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	max := int(ttl.Seconds())
	if ttl < 0 {
		max = -1
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.Config.PublicBaseURL, "https://"), SameSite: http.SameSiteStrictMode, MaxAge: max})
}
func cookieValue(r *http.Request, name string) string {
	c, e := r.Cookie(name)
	if e != nil {
		return ""
	}
	return c.Value
}
func (s *Service) trusted(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	for _, cidr := range s.Config.TrustedProxies {
		_, network, e := net.ParseCIDR(cidr)
		if e == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}
func (s *Service) clientIP(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	// Only a configured proxy may supply forwarding headers. Walk from the
	// trusted end so client-supplied leftmost entries cannot bypass rate limits.
	if s.trusted(r) {
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(parts) - 1; i >= 0; i-- {
			ip := net.ParseIP(strings.TrimSpace(parts[i]))
			if ip == nil {
				break
			}
			host = ip.String()
			trusted := false
			for _, cidr := range s.Config.TrustedProxies {
				_, n, _ := net.ParseCIDR(cidr)
				if n.Contains(ip) {
					trusted = true
					break
				}
			}
			if !trusted {
				break
			}
		}
	}
	return host
}
func fail(w http.ResponseWriter, status int, text string) {
	if status == 429 && w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", "60")
	}
	http.Error(w, text, status)
}
func jsonReply(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /chat", s.chat)
	mux.HandleFunc("GET /chat/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write([]byte(appJS))
	})
	mux.HandleFunc("GET /chat/style.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write([]byte(styleCSS))
	})
	mux.HandleFunc("GET /api/login/status", s.poll)
	mux.HandleFunc("GET /api/chat/messages/status", s.history)
	mux.HandleFunc("POST /api/chat/messages", s.action)
	mux.HandleFunc("POST /api/confirmations/{id}", s.action)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /api/requests", s.requests)
	mux.HandleFunc("GET /api/requests/{id}", s.requestStatus)
	mux.HandleFunc("POST /api/requests/{id}/cancel", s.cancelRequest)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		if strings.HasPrefix(s.Config.PublicBaseURL, "https://") {
			if !s.trusted(r) || r.Header.Get("X-Forwarded-Proto") != "https" {
				fail(w, 403, "HTTPS proxy required")
				return
			}
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if r.Method == http.MethodPost {
			// Exact configured origin plus non-simple request header: cross-site
			// forms and fetches cannot pass this check. Missing Origin fails closed.
			if r.Header.Get("Origin") != strings.TrimRight(s.Config.PublicBaseURL, "/") || r.Header.Get("X-Workshop-Request") != "1" {
				fail(w, 403, "Недопустимый источник запроса (CSRF).")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func (s *Service) chat(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/chat" {
		http.NotFound(w, r)
		return
	}
	if _, err := s.Authenticate(cookieValue(r, sessionCookie)); err == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(w, map[string]any{"Authenticated": true})
		return
	}
	code, nonce, err := s.CreateLogin(s.clientIP(r))
	if err != nil {
		if errors.Is(err, ErrLimit) {
			fail(w, 429, err.Error())
		} else {
			fail(w, 500, "Не удалось создать вход.")
		}
		return
	}
	// Reload replaces only this browser's pending login.
	if previous := cookieValue(r, pendingCookie); previous != "" {
		_, _ = s.DB.Exec(`UPDATE pending_web_logins SET status='blocked' WHERE browser_hash=? AND status='pending'`, hash(previous))
	}
	s.cookie(w, pendingCookie, nonce, s.Config.LoginCodeTTL)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = page.Execute(w, map[string]any{"Code": code, "TTL": int(s.Config.LoginCodeTTL.Seconds())})
}
func (s *Service) poll(w http.ResponseWriter, r *http.Request) {
	if !s.allow("poll:"+s.clientIP(r), 60) {
		fail(w, 429, ErrLimit.Error())
		return
	}
	token, err := s.Complete(cookieValue(r, pendingCookie))
	if err != nil {
		fail(w, 410, ErrLogin.Error())
		return
	}
	if token == "" {
		jsonReply(w, map[string]string{"status": "pending"})
		return
	}
	if _, err = s.Authenticate(token); err != nil {
		fail(w, 403, "Доступ закрыт.")
		return
	}
	s.cookie(w, sessionCookie, token, s.Config.SessionTTL)
	s.cookie(w, pendingCookie, "", -time.Second)
	jsonReply(w, map[string]string{"status": "authenticated"})
}
func (s *Service) session(w http.ResponseWriter, r *http.Request) (Session, bool) {
	session, err := s.Authenticate(cookieValue(r, sessionCookie))
	if err != nil {
		fail(w, 401, "Сессия истекла или доступ закрыт. Откройте /chat.")
		return Session{}, false
	}
	return session, true
}
func (s *Service) history(w http.ResponseWriter, r *http.Request) {
	ss, ok := s.session(w, r)
	if !ok {
		return
	}
	if !s.allow("history:"+hash(cookieValue(r, sessionCookie)), 60) {
		fail(w, 429, ErrLimit.Error())
		return
	}
	out, err := s.History(ss.ID)
	if err != nil {
		fail(w, 500, "Не удалось загрузить историю.")
		return
	}
	jsonReply(w, out)
}
func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	ss, ok := s.session(w, r)
	if !ok {
		return
	}
	if err := s.Revoke(ss.ID); err != nil {
		fail(w, 500, "Ошибка выхода.")
		return
	}
	s.cookie(w, sessionCookie, "", -time.Second)
	jsonReply(w, map[string]string{"status": "logged_out"})
}

var page = template.Must(template.New("chat").Parse(`<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>WORKSHOP_AGENT · Чат</title><link rel="stylesheet" href="/chat/style.css"><script src="/chat/app.js" defer></script></head><body><main><header><div><small>WORKSHOP_AGENT</small><h1>{{if .Authenticated}}Мастерская · Чат{{else}}Вход через Telegram{{end}}</h1></div>{{if .Authenticated}}<button id="logout">Выйти</button>{{end}}</header>{{if .Authenticated}}<section id="requests" aria-live="polite"></section><section id="history" aria-live="polite" aria-label="История диалога"></section><form id="send"><label for="text">Сообщение агенту</label><textarea id="text" rows="3" required placeholder="Задайте вопрос или опишите задачу"></textarea><button type="submit">Отправить</button></form>{{else}}<section id="login"><p>Отправьте команду существующему боту в личном чате:</p><pre>/login {{.Code}}</pre><p>Код действует {{.TTL}} сек. Подтверждайте только вход, который начали сами.</p><p id="waiting" role="status">Ожидаем подтверждение в Telegram…</p><a href="/chat">Получить новый код</a></section>{{end}}<p id="error" role="alert"></p><footer>Ваш помощник в работе мастерской</footer></main></body></html>`))

const appJS = `'use strict';
const $=id=>document.getElementById(id);let working=false;let pendingSend=null;
async function request(url,body,key){const options=body===undefined?{}:{method:'POST',headers:{'Content-Type':'application/json','X-Workshop-Request':'1'},body:JSON.stringify(body)};if(key)options.headers['Idempotency-Key']=key;const r=await fetch(url,options);if(!r.ok)throw Error(await r.text());return r.json();}
function error(e){$('error').textContent=e.message;}
function formatted(node,text){for(const [i,part] of text.split(/(\*\*[^*]+\*\*|\x60[^\x60]+\x60)/g).entries()){let el;if(part.startsWith('**')&&part.endsWith('**')){el=document.createElement('strong');el.textContent=part.slice(2,-2);}else if(part.startsWith('\x60')&&part.endsWith('\x60')){el=document.createElement('code');el.textContent=part.slice(1,-1);}else{el=document.createTextNode(part);}node.append(el);}}
let last='';async function history(){try{const list=await request('/api/chat/messages/status');const key=JSON.stringify(list);if(key===last)return;last=key;const box=$('history');box.replaceChildren();for(const m of list){const card=document.createElement('article');card.className=m.role;const label=document.createElement('small');label.textContent=m.role==='user'?'Вы':m.role==='error'?'Ошибка':'WORKSHOP_AGENT';const text=document.createElement('div');formatted(text,m.text);card.append(label,text);for(const b of m.buttons||[]){const button=document.createElement('button');button.textContent=b.text;button.onclick=()=>send('/api/confirmations/'+encodeURIComponent(b.id),{});card.append(button);}box.append(card);}box.scrollTop=box.scrollHeight;}catch(e){error(e);}}
async function send(url,body){if(working)return;working=true;$('error').textContent='';for(const b of document.querySelectorAll('button'))b.disabled=true;try{const fingerprint=url+JSON.stringify(body);if(!pendingSend||pendingSend.fingerprint!==fingerprint)pendingSend={fingerprint,key:crypto.randomUUID()};await request(url,body,pendingSend.key);pendingSend=null;await requests();if(body.text)$('text').value='';await history();}catch(e){error(e);}finally{working=false;for(const b of document.querySelectorAll('button'))b.disabled=false;}}
async function requests(){try{const list=await request('/api/requests');const box=$('requests');box.replaceChildren();const labels={QUEUED:'В очереди',RUNNING:'Выполняется',COMPLETED:'Готово',FAILED:'Ошибка',CANCELLED:'Отменено'};for(const r of list){const line=document.createElement('div');line.textContent=(labels[r.status]||r.status)+(r.error_code?' · '+r.error_code:'');if(r.status==='QUEUED'||r.status==='RUNNING'){const b=document.createElement('button');b.textContent='Отменить';b.onclick=async()=>{try{await request('/api/requests/'+encodeURIComponent(r.request_id)+'/cancel',{});await requests();}catch(e){error(e);}};line.append(b);}box.append(line);}}catch(e){error(e);}}
if($('login')){const timer=setInterval(async()=>{try{const s=await request('/api/login/status');if(s.status==='authenticated'){clearInterval(timer);location.replace('/chat');}}catch(e){clearInterval(timer);$('waiting').textContent='Вход не завершён.';error(e);}},2000);}
else{$('send').onsubmit=e=>{e.preventDefault();if(new TextEncoder().encode($('text').value).length>12000){error(Error('Максимум 12000 UTF-8 байт'));return;}send('/api/chat/messages',{text:$('text').value});};$('logout').onclick=async()=>{try{await request('/logout',{});location.replace('/chat');}catch(e){error(e);}};history();requests();setInterval(()=>{history();requests();},3000);}
`
const styleCSS = `:root{font-family:system-ui,sans-serif;color:#20322b;background:#f1f4ee}*{box-sizing:border-box}body{margin:0}main{max-width:920px;margin:auto;padding:24px}header{display:flex;align-items:center;justify-content:space-between;gap:16px;border-bottom:1px solid #cad5c8;margin-bottom:22px}h1{font-size:clamp(22px,4vw,32px);margin:8px 0 24px}small{font-size:12px;letter-spacing:.06em;color:#536b5f}button{border:1px solid #38644d;background:#38644d;color:white;border-radius:9px;padding:11px 18px;cursor:pointer;font:inherit;margin:6px 8px 0 0}button:disabled{opacity:.5;cursor:wait}button:focus-visible,textarea:focus-visible,a:focus-visible{outline:3px solid #ca942e;outline-offset:3px}#history{height:58vh;min-height:240px;overflow:auto;padding:6px}article{background:white;border:1px solid #d5dfd3;border-radius:12px;padding:16px;margin:0 40px 14px 0;overflow-wrap:anywhere}article>div{white-space:pre-wrap;line-height:1.6;margin-top:8px}article.user{background:#e1ebdf;margin:0 0 14px 40px}article.error,#error{color:#a13030}label{display:block;margin:16px 0 8px}textarea{width:100%;font:inherit;border:1px solid #9fae9c;border-radius:10px;padding:12px;resize:vertical}#login{background:white;padding:24px;border-radius:12px}pre{font-size:clamp(20px,5vw,30px);background:#edf2e9;padding:20px;border-radius:8px;overflow:auto}a{color:#25563b}footer{font-size:12px;color:#647461;margin-top:24px}code{background:#e8ede5;padding:2px 4px;border-radius:3px}@media(max-width:520px){main{padding:14px}article{margin-right:12px}article.user{margin-left:12px}#history{height:52vh}}`

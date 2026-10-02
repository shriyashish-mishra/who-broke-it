/* Who Broke It? project site. Vanilla JS, no dependencies, no network calls.
   Every transcript comes from data.js, which scripts/site-data.py captures from real runs of the demos. */
(function () {
  'use strict';
  const D = window.WBI_DEMO, T = D.t;
  { const ls = T.intents.split('\n'), k = ls.findIndex(l => /CONTRACT_NOT_OWNED/.test(l));
    T.intents1 = (k > 0 ? ls.slice(0, k) : ls).join('\n').trim(); T.intents2 = (k > 0 ? ls.slice(k) : []).join('\n').trim(); }
  const $ = (s, r = document) => r.querySelector(s), $$ = (s, r = document) => [...r.querySelectorAll(s)];
  const esc = s => String(s).replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
  const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;
  if (reduce) document.documentElement.classList.add('nomotion');
  const sleep = ms => new Promise(r => setTimeout(r, reduce ? 0 : ms));

  /* ---------- terminal ---------- */
  function cls(line) {
    const t = line.trim();
    if (/^(✓|✔)/.test(t)) return 'ok';
    if (/^(✗|✖)/.test(t) || /\bBLOCK\b/.test(t) || /^FAIL\b/.test(t) || /Status: FAILED/.test(t)) return 'no';
    if (/^(⚠|!)/.test(t) || /\bWARN\b/.test(t)) return 'wn';
    if (/^(WHO BROKE IT\?|BLAST RADIUS|EXECUTION PLAN|DRIFT REPORT|WAVE \d|WBI CHECK|PROJECT:|ATTENTION|ACTIVE|READY|BLOCKED$|TASK-\d+ (HANDOFF|VERIFICATION)|# TASK|##+ )/.test(t)) return 'hd';
    if (/^\$ /.test(t)) return 'bl';
    if (/^(Last significant|Direct:|Indirect:|Active agents:|Risk:)/.test(t)) return 'hd';
    return '';
  }
  function termInner(cmd, text) {
    let lines = text.replace(/\s+$/, '').split('\n');
    if (lines[0].replace(/^\$ /, '') === cmd.replace(/\s+#.*$/, '')) lines = lines.slice(1).filter((l, i) => i || l.trim());
    return `<div class="bar"><i></i><i></i><i></i><span>$ ${esc(cmd)}</span></div><pre>${lines.map(l => `<span class="ln ${cls(l)}">${esc(l) || ' '}</span>`).join('')}</pre>`;
  }
  async function reveal(term, perLine) {
    const lns = $$('.ln', term);
    const step = perLine ?? Math.max(8, Math.min(45, 1500 / Math.max(lns.length, 1)));
    lns.forEach(l => l.classList.remove('in'));
    for (const l of lns) { l.classList.add('in'); await sleep(step); }
  }
  function mountTerm(el, cmd, text) { el.className = (el.className.includes('term') ? el.className : el.className + ' term'); el.innerHTML = termInner(cmd, text); }

  const io = 'IntersectionObserver' in window ? new IntersectionObserver(es => es.forEach(e => {
    if (e.isIntersecting) { io.unobserve(e.target); e.target._go && e.target._go(); }
  }), { threshold: .25 }) : null;
  function onVisible(el, fn) { el._go = fn; io ? io.observe(el) : fn(); }

  $$('.term[data-key]').forEach(el => {
    const key = el.dataset.key, cmd = el.dataset.cmd || '';
    if (!(key in T)) return;
    mountTerm(el, cmd, T[key]);
    onVisible(el, () => reveal(el));
  });

  /* ---------- copy buttons ---------- */
  $$('[data-copy]').forEach(box => {
    const b = $('button', box);
    b.addEventListener('click', async () => {
      const text = box.dataset.copy;
      try { await navigator.clipboard.writeText(text); } catch (e) {
        const ta = document.createElement('textarea'); ta.value = text; document.body.appendChild(ta); ta.select(); try { document.execCommand('copy'); } catch (_) {} ta.remove();
      }
      b.textContent = 'copied'; b.classList.add('ok'); setTimeout(() => { b.textContent = 'copy'; b.classList.remove('ok'); }, 1400);
    });
  });

  /* ---------- the standup ---------- */
  const chat = $('#chat');
  onVisible(chat, async () => { for (const m of $$('.m', chat)) { m.classList.add('show'); await sleep(750); } });

  /* ---------- how it works ---------- */
  const STEPS = [
    { t: 'Plan', cmd: 'wbi simulate', text: T.plan.split('\n').slice(0, 27).join('\n') + '\n  …\n(waves 4 to 7 continue)', note: '`wbi plan` reads your repo and writes the Engineering Graph; `simulate` shows the waves, the critical path and any overlapping scopes before a single agent starts.' },
    { t: 'Claim', cmd: 'wbi claim TASK-007   # Gemini, a little early', text: T.blocked, note: 'Blocked tasks say exactly why. Claims are exclusive, even across machines.' },
    { t: 'Declare intent', cmd: 'wbi intent declare MODIFY src/api/billing/plans.ts --task TASK-003', text: T.intents1, note: 'Checked against the task\'s scope, who owns the area, contract ownership and what other agents are doing, before any code exists.' },
    { t: 'Hand off', cmd: 'wbi handoff TASK-007 --contract "BillingStatus=now supports paused" …', text: T.handoff, note: 'Changed files come from git, not from the agent\'s word. A contract change is versioned and propagated to every task it affects.' },
    { t: 'Verify', cmd: 'wbi handoff TASK-010 …', text: T.judge, note: 'The Judge found a constitution violation the agent had "forgotten" about. The task goes back to IN_PROGRESS.' },
    { t: 'Tell the affected', cmd: 'wbi inbox   # Gemini, on another machine', text: T.inbox + '\n\n' + T.teaminbox, note: 'The agent building on the old contract is told, with the reason, and its work packet now carries a banner until it acknowledges.' },
    { t: 'Open the PR', cmd: 'wbi pr TASK-007 --dry-run', text: T.pr, note: 'The body is built from the handoff: requirement, files (from git), contract changes, verification, what is affected.' },
    { t: 'Merge gate', cmd: 'wbi check --base main --head wbi/TASK-010', text: T.gate, note: 'The same checks run in CI as a GitHub Action, with inline annotations on the pull request.' },
    { t: 'Ask who broke it', cmd: 'wbi blame src/api/billing/', text: T.blame + '\n\n' + T.why, note: 'Task, agent, requirement, decision and commit, including unmerged work.' },
  ];
  const stepsEl = $('#steps'), stage = $('#stageterm');
  STEPS.forEach((s, i) => {
    const li = document.createElement('li');
    li.innerHTML = `<button role="tab" aria-selected="${i === 0}" data-i="${i}"><b>${String(i + 1).padStart(2, '0')}</b>${esc(s.t)}</button>`;
    stepsEl.appendChild(li);
  });
  function showStep(i) {
    const s = STEPS[i];
    $$('#steps button').forEach(b => b.setAttribute('aria-selected', b.dataset.i == i));
    $('#stagehead').textContent = `STEP ${i + 1} OF ${STEPS.length} · ${s.t.toUpperCase()}`;
    mountTerm(stage, s.cmd, s.text); stage.classList.add('big');
    $('#stagenote').innerHTML = esc(s.note).replace(/`([^`]+)`/g, '<code>$1</code>');
    reveal(stage, 14);
  }
  stepsEl.addEventListener('click', e => { const b = e.target.closest('button'); if (b) showStep(+b.dataset.i); });
  showStep(0);

  /* ---------- engineering graph ---------- */
  const tasks = D.tasks, byId = Object.fromEntries(tasks.map(t => [t.id, t]));
  const level = {}; const lv = id => level[id] ??= (byId[id].dependsOn.filter(d => byId[d]).reduce((m, d) => Math.max(m, lv(d) + 1), 0));
  tasks.forEach(t => lv(t.id));
  const cols = []; tasks.forEach(t => (cols[level[t.id]] ??= []).push(t));
  const NW = 150, NH = 44, GX = 40, GY = 12, pos = {};
  cols.forEach((c, i) => c.forEach((t, j) => pos[t.id] = { x: 10 + i * (NW + GX), y: 10 + j * (NH + GY) }));
  const GW = 20 + cols.length * (NW + GX) - GX, GH = 20 + Math.max(...cols.map(c => c.length)) * (NH + GY) - GY;
  const anc = id => { const o = new Set(), q = [id]; while (q.length) for (const d of byId[q.pop()].dependsOn) if (byId[d] && !o.has(d)) { o.add(d); q.push(d); } return o; };
  const dec = id => { const o = new Set(), q = [id]; while (q.length) { const c = q.pop(); for (const t of tasks) if (t.dependsOn.includes(c) && !o.has(t.id)) { o.add(t.id); q.push(t.id); } } return o; };
  let sel = null, hl = null;
  const g = $('#g'); g.setAttribute('viewBox', `0 0 ${GW} ${GH}`);
  function draw() {
    const up = sel ? anc(sel) : new Set(), down = sel ? dec(sel) : new Set();
    let h = '';
    for (const t of tasks) for (const d of t.dependsOn) {
      const a = pos[d], b = pos[t.id], x1 = a.x + NW, y1 = a.y + NH / 2, x2 = b.x, y2 = b.y + NH / 2, mx = (x1 + x2) / 2;
      let c = 'edge';
      if (sel) c += (t.id === sel || d === sel || (up.has(t.id) && up.has(d)) || (down.has(t.id) && down.has(d))) ? ' hot' : ' dim';
      if (hl) c += ((t.id === hl.origin || d === hl.origin) || (hl.all.has(t.id) && (hl.all.has(d) || d === hl.origin))) ? ' hot' : ' dim';
      h += `<path class="${c}" d="M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2},${y2}"/>`;
    }
    for (const t of tasks) {
      const p = pos[t.id]; let c = `node s-${t.display}`;
      if (hl) c += t.id === hl.origin ? ' origin' : hl.all.has(t.id) ? ' down' : ' dim';
      else if (sel) c += t.id === sel ? ' sel' : up.has(t.id) ? ' up' : down.has(t.id) ? ' down' : ' dim';
      const title = t.title.length > 20 ? t.title.slice(0, 19) + '…' : t.title;
      h += `<g class="${c}" data-id="${t.id}" transform="translate(${p.x},${p.y})"><title>${esc(t.id + ' ' + t.title + ' · ' + t.display)}</title><rect width="${NW}" height="${NH}"/><text class="id" x="9" y="15">${esc(t.id)}${t.display === 'DONE' ? ' ✓' : ''}${t.agent ? ' · ' + esc(t.agent.split('@')[0]) : ''}</text><text class="t" x="9" y="33">${esc(title)}</text></g>`;
    }
    g.innerHTML = h;
  }
  const chips = (l, k) => l.length ? `<div class="chips">${l.map(x => `<span class="chip ${k || ''}">${esc(x)}</span>`).join('')}</div>` : '<span class="dim">none</span>';
  function info() {
    const el = $('#ginfo');
    if (hl) {
      const colr = hl.risk === 'HIGH' ? 'var(--red)' : hl.risk === 'MEDIUM' ? 'var(--warn)' : 'var(--green)';
      const apol = hl.direct.length + hl.indirect.length ? `Apologise to the owners of ${[...hl.direct, ...hl.indirect].slice(0, 4).join(', ')}${hl.direct.length + hl.indirect.length > 4 ? '…' : ''}.` : 'Nobody to apologise to. Suspiciously clean.';
      el.innerHTML = `<p><b>${esc(hl.name)}</b> is owned by <b>${esc(hl.origin)}</b>. Blast radius <span class="pill" style="color:${colr}">${hl.risk}</span> · ${apol}</p><p class="dim">direct consumers</p>${chips(hl.direct, 'd')}<p class="dim">indirect (downstream of those)</p>${chips(hl.indirect, 'i')}`;
    } else if (sel) {
      const t = byId[sel];
      el.innerHTML = `<p><b>${esc(t.id)} ${esc(t.title)}</b> <span class="pill dim">${t.display}</span></p><p class="dim">${esc(t.goal || '')}</p><p>depends on</p>${chips(t.dependsOn)}<p>provides</p>${chips(t.provides)}<p>consumes</p>${chips(t.consumes)}`;
    } else el.innerHTML = '<p class="dim">Click a task to trace what it depends on (blue) and what depends on it (red). Break a contract to see its blast radius. We\'ll tell you who to apologise to.</p>';
    $$('.cbtn').forEach(b => b.setAttribute('aria-pressed', !!hl && b.dataset.c === hl.name));
  }
  g.addEventListener('click', e => { const n = e.target.closest('.node'); sel = n && n.dataset.id !== sel ? n.dataset.id : null; hl = null; draw(); info(); });
  const cb = $('#contractbtns');
  D.contracts.forEach(c => {
    const b = document.createElement('button'); b.className = 'cbtn'; b.dataset.c = c.name; b.type = 'button'; b.textContent = c.name; b.setAttribute('aria-pressed', 'false');
    b.onclick = () => { if (hl && hl.name === c.name) { hl = null; } else { const bl = D.blast[c.name]; sel = null; hl = { name: c.name, origin: c.providedBy, direct: bl.direct, indirect: bl.indirect, risk: bl.risk, all: new Set([...bl.direct, ...bl.indirect]) }; } draw(); info(); };
    cb.appendChild(b);
  });
  draw(); info();

  /* ---------- the incident ---------- */
  const alertbox = $('#alertbox'), lane = w => $(`.lane[data-who="${w}"]`);
  let busy = false;
  $('#replay').onclick = async () => {
    if (busy) return; busy = true; alertbox.innerHTML = '';
    const out = html => { alertbox.insertAdjacentHTML('beforeend', html + '<br>'); };
    const lines = T.intents1.split('\n').map(l => l.trim()).filter(Boolean);
    const blockLine = lines.find(l => /RESTRICTED_PATH/.test(l)), warns = lines.filter(l => /WARN/.test(l)), refused = lines.find(l => /NOT registered/.test(l));
    out('<span class="dim">gemini@devc is about to "tidy up" billing…</span>'); await sleep(800);
    out('<span class="dim">$ wbi intent declare MODIFY src/api/billing/plans.ts --task TASK-003</span>'); await sleep(900);
    lane('gemini').classList.add('alarm');
    if (blockLine) out(`<span class="blk">${esc(blockLine)}</span>`); await sleep(500);
    warns.forEach(w => out(`<span class="wrn">${esc(w)}</span>`)); await sleep(700);
    if (refused) out(`<span class="blk">${esc(refused)}</span>`); await sleep(500);
    out('<span class="okk">Stopped before a single line was written. Claude never even noticed.</span>');
    setTimeout(() => lane('gemini').classList.remove('alarm'), 1200); busy = false;
  };

  /* ---------- the race ---------- */
  const sa = $('#sa'), sb = $('#sb'), sr = $('#sr'), pa = $('#pa'), pb = $('#pb'), rbtn = $('#racebtn');
  function resetRace() { sa.className = sb.className = sr.className = 'state'; sa.textContent = sb.textContent = 'TASK-003: free'; sr.textContent = 'log: empty'; pa.className = 'pkt a'; pb.className = 'pkt b'; pa.textContent = pb.textContent = ''; }
  resetRace();
  rbtn.onclick = async () => {
    if (rbtn.disabled) return; rbtn.disabled = true; resetRace();
    sa.textContent = 'claimed locally…'; sb.textContent = 'claimed locally…'; sa.classList.add('mine'); sb.classList.add('mine'); await sleep(900);
    pa.classList.add('go'); pa.textContent = '↑'; await sleep(1100);
    sr.textContent = 'log: claude@alice claims TASK-003'; sr.classList.add('win'); sa.className = 'state win'; sa.textContent = 'push accepted · you own TASK-003'; await sleep(700);
    pb.classList.add('go'); pb.textContent = '↑'; await sleep(1100);
    pb.className = 'pkt b bounce'; pb.textContent = '✗'; await sleep(500);
    sb.className = 'state lose'; sb.textContent = 'push rejected (non-fast-forward) → fetch → lost the claim to claude@alice';
    await sleep(1200); rbtn.disabled = false;
  };

  /* ---------- the judge ---------- */
  const jterm = $('#judge .term'), gavel = $('#gavel'), gmsg = $('#gavelmsg');
  $('#judgebtn').onclick = async () => {
    gavel.classList.remove('fail'); gmsg.textContent = 'Objection: the agent says it\'s done.'; await sleep(900);
    gmsg.textContent = 'Sustained. Running the checks…';
    const lns = $$('.ln', jterm); lns.forEach(l => l.classList.remove('in', 'shake'));
    for (const l of lns) { l.classList.add('in'); if (/Constitution/.test(l.textContent) && /✗/.test(l.textContent)) { l.classList.add('shake'); gavel.classList.add('fail'); gmsg.textContent = 'Constitution C6 violated. Sent back to IN_PROGRESS.'; await sleep(900); } else await sleep(150); }
  };

  /* ---------- install tabs ---------- */
  $('#itabs').addEventListener('click', e => {
    const b = e.target.closest('button'); if (!b) return;
    $$('#itabs button').forEach(x => x.setAttribute('aria-selected', x === b));
    $$('.itab').forEach(p => p.hidden = p.id !== 'i-' + b.dataset.i);
  });
})();

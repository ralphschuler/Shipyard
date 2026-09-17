// Templates intentionally remain server-rendered, but this canonical list
// keeps legacy pages and newer pages on the exact same navigation contract.
const navigationEntries=[['/','▦','Übersicht'],['/projects','◫','Projekte'],['/boards','▤','Boards'],['/agents','◉','Agents'],['/automations','↯','Automationen',['/automations','/schedules','/webhooks']],['/skills','◇','Skills'],['/runs','▶','Runs'],['/audit','◷','Audit'],['/settings/providers','⚙','Einstellungen']];
// The server owns the translation vocabulary. Fetching it keeps legacy pages
// and dynamically inserted controls on one source of truth.
let shipyardTranslations={de:{},en:{}};
const cookieValue=name=>document.cookie.match(new RegExp(`(?:^|;\\s*)${name}=([^;]+)`))?.[1];
let activeShipyardLanguage=cookieValue('shipyard_language')==='en'?'en':'de';
const shipyardLanguage=()=>activeShipyardLanguage;
// Dynamic controls use the same source phrases as server-rendered legacy
// views. The English dictionary is also the intentional fallback when a new
// phrase has not been added to the selected language yet.
const trText=source=>shipyardTranslations[shipyardLanguage()]?.[source]||shipyardTranslations.en?.[source]||source;
const translationOriginals=new WeakMap();
const applyLanguage=()=>{const lang=shipyardLanguage();document.documentElement.lang=lang;const dictionary=shipyardTranslations[lang]||{};const walker=document.createTreeWalker(document.body,NodeFilter.SHOW_TEXT);const nodes=[];while(walker.nextNode())nodes.push(walker.currentNode);nodes.forEach(node=>{const original=translationOriginals.get(node)||node.nodeValue;translationOriginals.set(node,original);const value=original.trim();if(dictionary[value])node.nodeValue=original.replace(value,dictionary[value]);});document.querySelectorAll('[title],[aria-label],[placeholder]').forEach(node=>['title','aria-label','placeholder'].forEach(attribute=>{const originalKey=`${attribute}`;const original=translationOriginals.get(node)?.[originalKey]||node.getAttribute(attribute);const values=translationOriginals.get(node)||{};values[originalKey]=original;translationOriginals.set(node,values);if(original&&dictionary[original])node.setAttribute(attribute,dictionary[original]);}));document.querySelectorAll('[data-i18n-key]').forEach(node=>{const key=node.dataset.i18nKey;const visible=dictionary[key]||key;node.querySelector('span')?.replaceChildren(document.createTextNode(visible));node.title=visible;});};
let languageApplyTimer;
const observeLanguageChanges=()=>{new MutationObserver(()=>{clearTimeout(languageApplyTimer);languageApplyTimer=setTimeout(applyLanguage,0)}).observe(document.body,{childList:true,subtree:true})};
// Local preferences are deliberately progressive enhancement: the server UI
// remains fully usable when storage is unavailable.
const shipyardPrefs=(()=>{try{const saved=JSON.parse(localStorage.getItem('shipyard.preferences')||'{"theme":"system","shortcutHints":true}');return {theme:cookieValue('shipyard_theme')||saved.theme||'system',shortcutHints:(cookieValue('shipyard_shortcut_hints')||String(saved.shortcutHints))!=='false'}}catch(_){return {theme:cookieValue('shipyard_theme')||'system',shortcutHints:cookieValue('shipyard_shortcut_hints')!=='false'}}})();
const systemDark=matchMedia('(prefers-color-scheme:dark)');
const applyTheme=()=>{const selected=shipyardPrefs.theme||'system';document.documentElement.dataset.theme=selected==='system'?(systemDark.matches?'dark':'light'):selected};
applyTheme();
systemDark.addEventListener?.('change',()=>{if((shipyardPrefs.theme||'system')==='system')applyTheme()});
document.title=document.title.replace(/Taskboard/g,'Shipyard');
const loadTranslations=fetch('/api/i18n',{credentials:'same-origin'}).then(response=>response.ok?response.json():null).then(data=>{const dictionaries=data?.languages||{};if(data?.translations&&!dictionaries.en)dictionaries.en=data.translations;shipyardTranslations.de=dictionaries.de||{};shipyardTranslations.en=dictionaries.en||{};if(data?.language==='de'||data?.language==='en')activeShipyardLanguage=data.language;applyLanguage();observeLanguageChanges();return data}).catch(()=>{applyLanguage();observeLanguageChanges();return null});
document.addEventListener('change',event=>{if(!(event.target instanceof HTMLSelectElement)||event.target.name!=='language')return;activeShipyardLanguage=event.target.value==='en'?'en':'de';const value=activeShipyardLanguage;document.cookie=`shipyard_language=${value}; path=/; max-age=31536000; SameSite=Lax`;try{localStorage.setItem('shipyard.language',value)}catch(_){}if(value==='de'||Object.keys(shipyardTranslations.en).length)applyLanguage();else loadTranslations.then(applyLanguage);});
// Keep focus on the invoking control and make Escape opt-in for closable
// dialogs. Native showModal() supplies the remaining focus containment.
const modalReturnFocus=new WeakMap();
const nativeShowModal=HTMLDialogElement.prototype.showModal;
const isClosableDialog=dialog=>dialog.hasAttribute('data-closable')||!!dialog.querySelector('.close,[data-close-modal]');
const enhanceLegacyDialog=dialog=>{
 const article=dialog.querySelector(':scope > article');if(!article||article.querySelector(':scope > .dialog-body'))return;
 const header=article.querySelector(':scope > header'),footer=article.querySelector(':scope > footer');
 const body=[...article.children].filter(child=>child!==header&&child!==footer);
 if(!body.length)return;
 const wrapper=document.createElement('div');wrapper.className='dialog-body';body[0].before(wrapper);body.forEach(child=>wrapper.append(child));
 // Forms stay semantically intact, but their boxes must not become a second
 // scroll container after they have been moved into the shared body. A
 // second form (usually a destructive action) is marked as the dialog's
 // sticky action area so it remains available beside a long primary form.
 const forms=[...wrapper.querySelectorAll(':scope > form')];
 forms.forEach((form,index)=>form.classList.toggle('dialog-secondary-form',index>0));
};
const dialogFocusTarget=dialog=>dialog.querySelector('[autofocus]')||dialog.querySelector('.dialog-body button:not(.close):not([data-close-modal]),.dialog-body input,.dialog-body select,.dialog-body textarea,.dialog-body [tabindex]:not([tabindex="-1"])')||dialog.querySelector('.close,[data-close-modal]');
const openModal=(dialog,trigger=document.activeElement)=>{if(!dialog)return;if(trigger instanceof HTMLElement)modalReturnFocus.set(dialog,trigger);enhanceLegacyDialog(dialog);if(!dialog.open)nativeShowModal.call(dialog);requestAnimationFrame(()=>dialogFocusTarget(dialog)?.focus())};
HTMLDialogElement.prototype.showModal=function(){openModal(this)};
document.addEventListener('cancel',event=>{const dialog=event.target.closest?.('dialog');if(dialog&&!isClosableDialog(dialog))event.preventDefault()},true);
document.addEventListener('close',event=>{const dialog=event.target;if(!(dialog instanceof HTMLDialogElement))return;const trigger=modalReturnFocus.get(dialog);modalReturnFocus.delete(dialog);if(trigger?.isConnected)requestAnimationFrame(()=>trigger.focus())},true);
document.querySelectorAll('.brand-mark').forEach(mark=>{mark.textContent='SY';mark.title=trText('Shipyard')});
// Older server-rendered views predate the Shipyard favicon. Keep branding a
// shell responsibility so newly added extension pages cannot accidentally
// fall back to the browser default icon just because their template is small.
if(!document.querySelector('link[rel~="icon"]')){const favicon=document.createElement('link');favicon.rel='icon';favicon.href='/static/favicon.svg';document.head.append(favicon)}
// HTMX owns ordinary state changes. Bespoke JavaScript remains limited to the
// canvas, touch drag/drop, SSE and terminal output. Links stay full-page until
// every view has a fragment lifecycle, keeping touch navigation deterministic.
const enableHTMX=()=>{
 if(!window.htmx)return;
 const prepare=(root=document)=>root.querySelectorAll?.('form[method="post"]:not([data-no-htmx])').forEach(form=>{
   if(form.enctype==='multipart/form-data')return;
   form.setAttribute('hx-post',form.getAttribute('action')||location.pathname);
   form.setAttribute('hx-swap','none');
 });
 prepare();
 window.htmx.process(document.body);
 // Several task controls create their dialog only after a button press.
 // Observe those additions so no mutation silently falls back to a second
 // client-side transport model.
 new MutationObserver(records=>{for(const record of records)for(const node of record.addedNodes){if(node.nodeType!==1)continue;prepare(node);window.htmx.process(node)}}).observe(document.body,{childList:true,subtree:true});
};
const loadHTMX=()=>{
 if(window.htmx){enableHTMX();return}
 const script=document.createElement('script');script.src='/static/htmx-2.0.4.min.js';script.onload=enableHTMX;document.head.append(script);
};
loadHTMX();
document.querySelectorAll('.app-sidebar nav').forEach(nav=>{
 // Do not shuffle server-rendered anchors. Moving those nodes after first
 // paint made their visual order and their touch hit targets drift apart on
 // some Chromium tablet builds. Rebuild one immutable navigation list before
 // handlers are attached instead. The URL is the sole source of active state.
 const links=document.createDocumentFragment();
 navigationEntries.forEach(([href,icon,label,ownedRoutes])=>{
   const link=document.createElement('a');
   const routes=ownedRoutes||[href];
   const active=routes.some(route=>route==='/'?location.pathname==='/' : location.pathname===route||location.pathname.startsWith(`${route}/`));
   const visibleLabel=shipyardTranslations[shipyardLanguage()]?.[label]||label;
   link.dataset.i18nKey=label;link.href=href;link.title=visibleLabel;link.innerHTML=`<b>${icon}</b><span>${visibleLabel}</span>`;
   link.classList.toggle('active',active);
   if(active)link.setAttribute('aria-current','page');
   links.append(link);
 });
  nav.replaceChildren(links);

  // A site navigation is still normal links (Tab remains canonical). Arrow
  // keys only provide an extra roving convenience while focus is inside it.
  nav.addEventListener('keydown',event=>{
   if(!['ArrowDown','ArrowUp','Home','End'].includes(event.key))return;
   const items=[...nav.querySelectorAll('a')];const index=items.indexOf(document.activeElement);if(index<0)return;
   event.preventDefault();let next=index;
   if(event.key==='ArrowDown')next=(index+1)%items.length;if(event.key==='ArrowUp')next=(index-1+items.length)%items.length;if(event.key==='Home')next=0;if(event.key==='End')next=items.length-1;items[next].focus();
  });
});
// Legacy full-task templates are progressively upgraded too, so every agent
// question offers a human override while old bookmarked pages keep working.
document.querySelectorAll('.agent-interaction form[action^="/interactions/"]').forEach(form=>{
 if(form.querySelector('[name="freeform_answer"]'))return;
 const label=document.createElement('label');label.textContent=trText('Eigene oder ergänzende Antwort');
 const input=document.createElement('textarea');input.name='freeform_answer';input.placeholder=trText('Überschreibt oder ergänzt die Auswahl.');label.append(input);
 form.insertBefore(label,form.lastElementChild);
});
// Choice buttons are selections, not implicit submissions. This lets a person
// choose a proposed option, add context or an override, and submit one
// complete answer deliberately.
document.addEventListener('click',event=>{
 const choice=event.target.closest('.interaction-buttons button');if(!choice)return;
 event.preventDefault();const form=choice.closest('form');if(!form)return;
 const name=choice.name;if(!name)return;
 let value=form.querySelector(`input[type="hidden"][name="${CSS.escape(name)}"]`);
 if(!value){value=document.createElement('input');value.type='hidden';value.name=name;form.append(value)}
 value.value=choice.value;choice.closest('.interaction-buttons')?.querySelectorAll('button').forEach(button=>button.classList.toggle('is-selected',button===choice));
});
// Decisions outrank history: a person should never have to scroll through a
// completed conversation before seeing that an agent is waiting for them.
if(document.body.classList.contains('task-page')){
 const hero=document.querySelector('.task-hero');
 document.querySelectorAll('.agent-interaction').forEach(card=>hero?.after(card));
}
const shortcutHelp=()=>{let dialog=document.getElementById('shipyard-shortcuts');if(!dialog){dialog=document.createElement('dialog');dialog.id='shipyard-shortcuts';dialog.innerHTML=`<article><header><button class="close" aria-label="${trText('Schließen')}"></button><h2>${trText('Tastatursteuerung')}</h2></header><dl><dt>Tab / Umschalt+Tab</dt><dd>${trText('Zum nächsten oder vorherigen Bedienelement')}</dd><dt>${trText('Eingabe / Leertaste')}</dt><dd>${trText('Link, Button oder Auswahl auslösen')}</dd><dt>↑ / ↓, Pos1 / Ende</dt><dd>${trText('Navigation in der Seitenleiste')}</dd><dt>Escape</dt><dd>${trText('Dialog oder mobile Navigation schließen')}</dd></dl><label><input type="checkbox" data-shortcut-hints> ${trText('Hinweise zu Shortcuts anzeigen')}</label></article>`;document.body.append(dialog);dialog.querySelector('.close').addEventListener('click',()=>dialog.close());dialog.querySelector('[data-shortcut-hints]').checked=shipyardPrefs.shortcutHints!==false;dialog.querySelector('[data-shortcut-hints]').addEventListener('change',e=>{shipyardPrefs.shortcutHints=e.target.checked;try{localStorage.setItem('shipyard.preferences',JSON.stringify(shipyardPrefs))}catch(_){}})}openModal(dialog)};
document.addEventListener('keydown',event=>{if(event.key==='?'&&!/input|textarea|select/i.test(event.target.tagName)){event.preventDefault();shortcutHelp()}});
document.querySelectorAll('.app-sidebar').forEach(sidebar=>{
 const shell=sidebar.closest('.app-shell');
 const desktop=matchMedia('(min-width:801px)');
 const toggle=document.createElement('button');toggle.type='button';toggle.className='mobile-nav-toggle';toggle.setAttribute('aria-label',trText('Navigation öffnen'));toggle.innerHTML='<span></span><span></span><span></span>';
 sidebar.prepend(toggle);
 // Storage is a preference only. It must never make the toggle unusable in a
 // browser that blocks local storage (for example private or embedded views).
 const readOpen=()=>{try{return localStorage.getItem('taskboard.sidebar.expanded')==='true'}catch(_){return false}};
 const writeOpen=open=>{try{localStorage.setItem('taskboard.sidebar.expanded',String(open))}catch(_){}};
 const applyDesktop=open=>{const expanded=open??readOpen();shell?.classList.toggle('sidebar-expanded',expanded);toggle.setAttribute('aria-expanded',String(expanded));toggle.setAttribute('aria-label',trText(expanded?'Navigation einklappen':'Navigation ausklappen'))};
 const close=()=>{sidebar.classList.remove('mobile-open');if(!desktop.matches){toggle.setAttribute('aria-expanded','false');toggle.setAttribute('aria-label',trText('Navigation öffnen'))}};
 const apply=()=>{if(desktop.matches)applyDesktop();else{shell?.classList.remove('sidebar-expanded');close()}};
 apply();desktop.addEventListener?.('change',apply);
 toggle.addEventListener('click',event=>{event.preventDefault();event.stopPropagation();if(desktop.matches){const open=!shell?.classList.contains('sidebar-expanded');writeOpen(open);applyDesktop(open);return}const open=sidebar.classList.toggle('mobile-open');toggle.setAttribute('aria-expanded',String(open));toggle.setAttribute('aria-label',trText(open?'Navigation schließen':'Navigation öffnen'))});
 sidebar.querySelectorAll('a').forEach(link=>link.addEventListener('click',close));
 document.addEventListener('click',event=>{if(!sidebar.contains(event.target))close()});
 document.addEventListener('keydown',event=>{if(event.key==='Escape')close()});
});
const toastRegion=document.createElement('div');toastRegion.className='toast-region';toastRegion.setAttribute('aria-live','polite');document.body.append(toastRegion);
function notify(message,kind='success'){const item=document.createElement('div');item.className=`toast ${kind}`;item.textContent=message;toastRegion.append(item);setTimeout(()=>item.remove(),3200)}
// The worker, rather than a saved form value, owns Codex' execution policy.
// Keep the same guard in the browser as on the server so an obvious invalid
// adapter value gets an immediate accessible explanation without generating a
// noisy expected 4xx request in the browser console. The server validation is
// deliberately still authoritative for MCP and non-JavaScript requests.
document.addEventListener('submit',event=>{const form=event.target;if(!(form instanceof HTMLFormElement)||!form.matches('form[action$="/settings/providers/codex"]'))return;const field=form.elements.namedItem('command');const parts=String(field?.value||'').trim().split(/\s+/).filter(Boolean);if(parts.length<=2&&!(parts.length===2&&parts[1]!=='exec'))return;event.preventDefault();event.stopImmediatePropagation();notify('Codex-Kommando darf nur die Ausführungsdatei und optional „exec“ enthalten. Optionen gehören in Zusatzoptionen.','error');field?.focus()},{capture:true});
// HTMX forms intentionally keep the current view until a successful redirect.
// Surface all rejected mutations in the same view; otherwise a 4xx response
// (invalid workflow, stale CSRF, unavailable agent) looks like a dead button.
document.body.addEventListener('htmx:responseError',event=>{const xhr=event.detail?.xhr;let message=(xhr?.responseText||trText('Änderung konnte nicht gespeichert werden.')).replace(/<[^>]*>/g,' ').replace(/\s+/g,' ').trim();if(!message)message=trText('Änderung konnte nicht gespeichert werden.');notify(message.slice(0,420),'error')});
document.body.addEventListener('htmx:sendError',()=>notify(trText('Verbindung zum Server fehlgeschlagen. Bitte erneut versuchen.'),'error'));
// The dashboard separates current action from historical charts. It is
// populated asynchronously so live metric updates never delay first render.
// Only the actual overview owns the attention feed. Other pages reuse the
// dashboard layout classes for spacing, but must never receive dashboard UI.
const dashboard=document.querySelector('[data-dashboard-overview]');
if(dashboard){fetch('/dashboard/attention').then(response=>response.ok?response.json():null).then(attention=>{
 if(!attention)return;
 const items=[['Blockierte Tasks',attention.BlockedTasks,'/boards'],['Aktuell fehlgeschlagen · 7 Tage',attention.FailedRuns7d,'/runs'],['Offene Agent-Fragen',attention.OpenInteractions,'/boards'],['Fällig in 24 Stunden',attention.DueNext24h,'/boards']];
 const section=document.createElement('section');section.className='attention-panel';section.setAttribute('aria-label',trText('Braucht Aufmerksamkeit'));
 section.innerHTML=`<header><div><p class="section-kicker">${trText('Jetzt handeln')}</p><h2>${trText('Braucht Aufmerksamkeit')}</h2></div><p>${trText('Nur offene Punkte, keine Historie.')}</p></header><div class="attention-grid">${items.map(([label,count,href])=>`<a href="${href}"><strong>${count}</strong><span>${trText(label)}</span></a>`).join('')}</div>`;
 const notifications=dashboard.querySelector('.notifications');(notifications||dashboard).before(section);
}).catch(()=>{})}
// Provider tests are intentionally token-free checks. They verify the local
// adapter or configured API secret before an agent can be assigned work.
document.querySelectorAll('.provider-card form[action^="/settings/providers/"]').forEach(form=>{const path=form.action.replace(location.origin,'');if(!/^\/settings\/providers\/[^/]+$/.test(path))return;const button=document.createElement('button');button.type='button';button.className='secondary';button.textContent=trText('Verbindung testen');const result=document.createElement('small');result.className='provider-test-result';result.setAttribute('role','status');button.addEventListener('click',async()=>{button.disabled=true;result.className='provider-test-result';result.textContent=trText('Prüfe …');try{const response=await fetch(`${path}/test`,{method:'POST',headers:csrfHeaders()});const data=await response.json().catch(()=>null);if(!response.ok)throw new Error(data||'');result.classList.add('is-success');result.textContent=data?.result||trText('Provider ist erreichbar.')}catch(_){result.classList.add('is-error');result.textContent=trText('Provider-Test fehlgeschlagen. Prüfe Adapter, Secret und Server-Log.')}finally{button.disabled=false}});form.querySelector('footer')?.prepend(button);form.querySelector('footer')?.before(result)});
// A run log is the terminal view; the compact trace above it explains why the
// run exists and which delivery decision remains without duplicating logs.
const traceLog=document.querySelector('[data-run-log-src]');
document.addEventListener('toggle',async event=>{const entry=event.target;if(!(entry instanceof HTMLDetailsElement)||!entry.open||!entry.dataset.runLogEntry||entry.dataset.loaded)return;const output=entry.querySelector('pre');if(!output)return;entry.dataset.loaded='true';output.hidden=false;output.textContent=trText('Vollständige Ausgabe wird geladen …');try{const response=await fetch(entry.dataset.runLogEntry);if(!response.ok)throw new Error();output.textContent=await response.text()}catch(_){output.textContent=trText('Die vollständige Ausgabe konnte nicht geladen werden.')}},true);
// The run console starts with a compact tail. Older output is fetched in
// chronological pages on demand, so a noisy dependency install cannot make
// the active page, browser history or live SSE refresh unresponsive.
document.addEventListener('click',async event=>{const button=event.target.closest('[data-run-log-older]');if(!button||button.disabled)return;const host=button.closest('[data-run-log-src]');if(!host)return;const before=button.dataset.before;if(!/^\d+$/.test(before||''))return;button.disabled=true;button.textContent=trText('Ältere Ausgabe wird geladen …');try{const response=await fetch(`${host.dataset.runLogSrc}?before=${encodeURIComponent(before)}`);if(!response.ok)throw new Error();const page=document.createElement('template');page.innerHTML=await response.text();button.closest('[data-run-log-page]')?.replaceWith(page.content)}catch(_){button.disabled=false;button.textContent=trText('Ältere Ausgabe erneut laden');notify(trText('Ältere Ausgabe konnte nicht geladen werden.'),'error')}});
if(traceLog){const match=traceLog.dataset.runLogSrc.match(/^\/runs\/([^/]+)\/logs$/);if(match){fetch(`/runs/${match[1]}/trace`).then(response=>response.ok?response.json():null).then(trace=>{
 if(!trace||!trace.Items?.length)return;
 const section=document.createElement('section');section.className='run-trace';const heading=document.createElement('h2');heading.textContent=trText('Ablauf');section.append(heading);const list=document.createElement('ol');
 trace.Items.forEach(item=>{const row=document.createElement('li'),title=document.createElement('strong'),meta=document.createElement('small'),detail=document.createElement('span');title.textContent=item.Kind;meta.textContent=new Date(item.At).toLocaleString(shipyardLanguage());detail.textContent=item.Detail;row.append(title,meta,detail);list.append(row)});section.append(list);traceLog.closest('section')?.before(section);
}).catch(()=>{})}}
// Delivery review stays next to the console: load the real patch on demand,
// then allow a human to reject it with task-level feedback and an optional
// fresh, isolated retry.
if(traceLog&&traceLog.dataset.runHasDiff==='true'){const match=traceLog.dataset.runLogSrc.match(/^\/runs\/([^/]+)\/logs$/);if(match){const runID=match[1],host=traceLog.closest('section');if(host){const review=document.createElement('section');review.className='delivery-review';review.innerHTML='<h2>Patch prüfen</h2><p>Der Diff wird nur auf Nachfrage aus dem isolierten Worktree geladen.</p>';const load=document.createElement('button');load.type='button';load.className='secondary';load.textContent='Diff laden';const output=document.createElement('pre');output.hidden=true;load.addEventListener('click',async()=>{load.disabled=true;load.textContent='Diff wird geladen …';try{const response=await fetch(`/runs/${runID}/diff`);if(!response.ok)throw new Error();output.textContent=await response.text()||'Keine Änderungen im Worktree.';output.hidden=false;load.textContent='Diff aktualisieren'}catch(_){output.textContent='Diff ist für diesen Run nicht verfügbar.';output.hidden=false;load.textContent='Diff laden'}finally{load.disabled=false}});review.append(load,output);const reject=document.createElement('button');reject.type='button';reject.className='contrast secondary';reject.textContent='Änderungen ablehnen';reject.addEventListener('click',()=>{const dialog=document.createElement('dialog');dialog.innerHTML=`<article><header><button type="button" class="close" aria-label="Schließen"></button><h2>Run ablehnen</h2><p>Das Feedback wird am Task gespeichert und dem nächsten Agentenversuch als Kontext gegeben.</p></header><form method="post" action="/runs/${runID}/reject"><label>Feedback<textarea name="feedback" required placeholder="Was soll der Agent ändern?"></textarea></label><label><input type="checkbox" name="restart" value="true" checked> Mit Feedback erneut starten</label><footer><button type="button" class="secondary">Abbrechen</button><button class="contrast">Ablehnen</button></footer></form></article>`;document.body.append(dialog);dialog.querySelectorAll('[data-close-modal],.close,button[type="button"]').forEach(button=>button.addEventListener('click',()=>dialog.close()));dialog.addEventListener('close',()=>dialog.remove());dialog.showModal()});review.append(reject);host.before(review)}}}
// A handoff is visible work, not an invisible reassignment. Existing agent
// choices from the task page are reused so only permitted active profiles can
// receive the contextual note and optional follow-up run.
if(document.body.classList.contains('task-page')){const taskMatch=location.pathname.match(/^\/tasks\/([^/]+)$/),agentSelect=document.querySelector('form[action$="/runs"] select[name="agent_id"]');if(taskMatch&&agentSelect){const trigger=document.createElement('button');trigger.type='button';trigger.className='secondary';trigger.textContent='An Agent übergeben';agentSelect.closest('form')?.append(trigger);trigger.addEventListener('click',()=>{const dialog=document.createElement('dialog'),options=[...agentSelect.options].map(option=>`<option value="${option.value}">${option.textContent}</option>`).join('');dialog.innerHTML=`<article><header><button type="button" class="close" aria-label="Schließen"></button><h2>Task übergeben</h2><p>Die Übergabe wird als Kommentar gespeichert und ist Teil des nächsten Agent-Kontexts.</p></header><form method="post" action="/tasks/${taskMatch[1]}/handoff"><label>Agent<select name="agent_id">${options}</select></label><label>Übergabehinweis<textarea name="note" required placeholder="Was wurde bereits geprüft, was ist der nächste Schritt?"></textarea></label><label><input type="checkbox" name="start" value="true" checked> Folge-Run sofort starten</label><footer><button type="button" class="secondary">Abbrechen</button><button>Übergabe speichern</button></footer></form></article>`;document.body.append(dialog);dialog.querySelectorAll('.close,button[type="button"]').forEach(button=>button.addEventListener('click',()=>dialog.close()));dialog.addEventListener('close',()=>dialog.remove());dialog.showModal()})}}
if(document.body.classList.contains('task-page')){const taskMatch=location.pathname.match(/^\/tasks\/([^/]+)$/),taskActions=document.querySelector('.task-actions');if(taskMatch&&taskActions){const trigger=document.createElement('button');trigger.type='button';trigger.className='secondary';trigger.textContent='Entscheidung anfordern';taskActions.append(trigger);trigger.addEventListener('click',()=>{const dialog=document.createElement('dialog');dialog.innerHTML=`<article><header><button type="button" class="close" aria-label="Schließen"></button><h2>Entscheidung anfordern</h2><p>Die Frage wird am Task festgehalten und auf dem Dashboard hervorgehoben.</p></header><form method="post" action="/tasks/${taskMatch[1]}/needs-decision"><label>Benötigte Entscheidung<textarea name="question" required placeholder="Welche Entscheidung wird benötigt und welche Optionen gibt es?"></textarea></label><footer><button type="button" class="secondary">Abbrechen</button><button>Entscheidung anfordern</button></footer></form></article>`;document.body.append(dialog);dialog.querySelectorAll('.close,button[type="button"]').forEach(button=>button.addEventListener('click',()=>dialog.close()));dialog.addEventListener('close',()=>dialog.remove());dialog.showModal()})}}
// State-changing requests carry the server-issued, per-session CSRF value.
// The capture listener also covers forms inserted later into the task drawer.
const csrfToken=()=>document.cookie.match(/(?:^|;\s*)taskboard_csrf=([^;]+)/)?.[1]||'';
const csrfHeaders=(headers={})=>{const token=csrfToken();return token?{...headers,'X-CSRF-Token':decodeURIComponent(token)}:headers};
document.addEventListener('submit',event=>{const form=event.target.closest('form');if(!form||['get','dialog'].includes((form.method||'get').toLowerCase()))return;const token=csrfToken();if(!token)return;let input=form.querySelector('input[name="csrf_token"]');if(!input){input=document.createElement('input');input.type='hidden';input.name='csrf_token';form.append(input)}input.value=decodeURIComponent(token)},true);
// PostgreSQL LISTEN/NOTIFY is exposed as SSE so every open view reflects
// changes made by another browser, MCP client or automation run.
if(window.EventSource){
 let refreshTimer,refreshPending=false;
 const runLog=document.querySelector('[data-run-log-src]');
 let runLogStatus='';
 const scrollRunLog=(force=false)=>{if(!runLog)return;const atEnd=runLog.scrollHeight-runLog.scrollTop-runLog.clientHeight<36;if(force||atEnd)runLog.scrollTop=runLog.scrollHeight};
 // Do not reload the document when a run finishes.  The log is the only
 // volatile projection on this legacy page, so replace just that fragment and
 // leave scroll position, dialogs and any in-progress form alone.
 const refreshRunLog=async()=>{if(!runLog||refreshPending)return;refreshPending=true;try{const response=await fetch(runLog.dataset.runLogSrc,{headers:{'X-Run-Log':'true'}});if(!response.ok)return;const nextStatus=response.headers.get('X-Run-Status')||'';const follow=runLog.scrollHeight-runLog.scrollTop-runLog.clientHeight<36;runLog.innerHTML=await response.text();scrollRunLog(follow);if(runLogStatus&&runLogStatus!==nextStatus&&!['queued','running'].includes(nextStatus))notify('Der Run-Status wurde aktualisiert.','success');runLogStatus=nextStatus}finally{refreshPending=false}};
 if(runLog){scrollRunLog(true);refreshRunLog();}
 const refresh=(change={})=>{if(refreshPending)return;clearTimeout(refreshTimer);refreshTimer=setTimeout(()=>{if(runLog){refreshRunLog();return}/* Legacy pages have no safe partial representation for every view.  Announce a data change without replacing the document; the React panel consumes the same event with selective API requests. */window.dispatchEvent(new CustomEvent('taskboard:legacy-data-change',{detail:change}));},650)};
 const live=new EventSource('/events');
 live.addEventListener('change',event=>{
   if(document.visibilityState!=='visible')return;
   let change={};try{change=JSON.parse(event.data)}catch(_){}
   if(change.table==='agent_run_logs'){if(runLog)refresh(change);return}
   if(runLog){if(change.table==='agent_runs')refresh(change);return}
   // Never perform a document reload for an SSE message.  The current React
   // app selectively refetches its affected API data; legacy pages merely
   // surface the non-disruptive update signal until they are migrated.
   refresh(change);
 });
}
document.querySelectorAll('[data-open-modal]').forEach(button=>button.addEventListener('click',()=>{const modal=document.getElementById(button.dataset.openModal);if(modal){const node=button.closest('.flow-node');if(node){modal.querySelector('[name=x]').value=node.dataset.x;modal.querySelector('[name=y]').value=node.dataset.y}openModal(modal,button)}}));
document.querySelectorAll('[data-close-modal]').forEach(button=>button.addEventListener('click',()=>button.closest('dialog').close()));
document.querySelectorAll('[data-template-detail]').forEach(button=>button.addEventListener('click',()=>{
 const modal=document.querySelector('#template-details');if(!modal)return;
 modal.querySelector('[data-template-detail-title]').textContent=button.dataset.templateTitle||'';
 modal.querySelector('[data-template-detail-body]').textContent=button.dataset.templateDetailText||'';
 modal.querySelector('[data-template-detail-columns]').textContent=`Spalten: ${button.dataset.templateColumns||''}`;
 openModal(modal,button);
}));
// An automation owns one board. Filtering columns in the dialog keeps the
// workflow graph understandable and mirrors the server-side integrity check.
const automationForm=document.querySelector('#new-rule form');
if(automationForm){
 const board=automationForm.querySelector('[name=board_id]');
 const columns=[...automationForm.querySelectorAll('[name=target_column_id],[name=success_column_id],[name=failure_column_id]')];
 const filterColumns=()=>columns.forEach(select=>[...select.options].forEach(option=>{
   if(!option.value)return;
   const allowed=option.dataset.board===board.value;
   option.hidden=!allowed;option.disabled=!allowed;
   if(!allowed&&option.selected)select.value='';
 }));
 board.addEventListener('change',filterColumns);filterColumns();
 const preview=document.createElement('button');preview.type='button';preview.className='secondary';preview.textContent='Betroffene Tasks prüfen';
 const result=document.createElement('div');result.className='automation-preview';result.setAttribute('aria-live','polite');
 preview.addEventListener('click',async()=>{const boardID=board.value,target=automationForm.querySelector('[name=target_column_id]').value;if(!boardID){result.textContent='Wähle zuerst ein Board aus.';return}preview.disabled=true;result.textContent='Vorschau wird geladen …';try{const response=await fetch(`/automations/preview?board_id=${encodeURIComponent(boardID)}&target_column_id=${encodeURIComponent(target)}`);const data=await response.json();if(!response.ok)throw new Error(data||'');const tasks=data.tasks||[];result.replaceChildren();const title=document.createElement('strong');title.textContent=tasks.length?`${tasks.length}${data.limited?'+':''} offene Task(s) würden passen.`:'Keine offenen Tasks passen aktuell.';result.append(title);if(tasks.length){const list=document.createElement('ul');tasks.forEach(task=>{const item=document.createElement('li');item.textContent=`${task.ColumnName}: ${task.Title}`;list.append(item)});result.append(list)}}catch(_){result.textContent='Vorschau konnte nicht geladen werden.'}finally{preview.disabled=false}});
 automationForm.querySelector('footer')?.before(preview,result);
}
let taskCard;
document.addEventListener('dragstart',event=>{taskCard=event.target.closest('[data-task]')});
document.addEventListener('dragover',event=>{if(event.target.closest('[data-dropzone]'))event.preventDefault()});
document.addEventListener('drop',async event=>{const zone=event.target.closest('[data-dropzone]');if(!zone||!taskCard)return;event.preventDefault();const body=new URLSearchParams({target_column_id:zone.dataset.dropzone});const response=await fetch(`/tasks/${taskCard.dataset.task}/move`,{method:'POST',headers:csrfHeaders({'Content-Type':'application/x-www-form-urlencoded','HX-Request':'true'}),body});if(response.ok){taskCard.outerHTML=await response.text();zone.appendChild(taskCard);notify('Aufgabe verschoben')}else notify('Dieser Wechsel ist nicht erlaubt','error')});

let touchTask,touchTimer,touchActive=false,touchStart;
const clearTouchTarget=()=>document.querySelectorAll('.touch-target').forEach(zone=>zone.classList.remove('touch-target'));
document.addEventListener('pointerdown',event=>{if(event.pointerType!=='touch')return;const card=event.target.closest('[data-task]');if(!card)return;touchTask=card;touchStart={x:event.clientX,y:event.clientY};touchTimer=setTimeout(()=>{touchActive=true;touchTask.classList.add('touch-dragging');touchTask.setPointerCapture?.(event.pointerId)},300)});
document.addEventListener('pointermove',event=>{if(event.pointerType!=='touch'||!touchTask)return;if(!touchActive&&Math.hypot(event.clientX-touchStart.x,event.clientY-touchStart.y)>12){clearTimeout(touchTimer);touchTask=null;return}if(!touchActive)return;event.preventDefault();clearTouchTarget();document.elementFromPoint(event.clientX,event.clientY)?.closest('[data-dropzone]')?.classList.add('touch-target')},{passive:false});
document.addEventListener('pointerup',async event=>{if(event.pointerType!=='touch'||!touchTask)return;clearTimeout(touchTimer);const card=touchTask;const active=touchActive;touchTask=null;touchActive=false;card.classList.remove('touch-dragging');const zone=document.elementFromPoint(event.clientX,event.clientY)?.closest('[data-dropzone]');clearTouchTarget();if(!active||!zone)return;event.preventDefault();const body=new URLSearchParams({target_column_id:zone.dataset.dropzone});const response=await fetch(`/tasks/${card.dataset.task}/move`,{method:'POST',headers:csrfHeaders({'Content-Type':'application/x-www-form-urlencoded','HX-Request':'true'}),body});if(response.ok){card.outerHTML=await response.text();zone.appendChild(card);notify('Aufgabe verschoben')}else notify('Dieser Wechsel ist nicht erlaubt','error')});
document.addEventListener('pointercancel',()=>{clearTimeout(touchTimer);touchTask?.classList.remove('touch-dragging');touchTask=null;touchActive=false;clearTouchTarget()});

const canvas=document.querySelector('.flow-canvas');
if(canvas){
 const toolbar=canvas.previousElementSibling;
 let zoom=1;
 const zoomValue=toolbar?.querySelector('[data-canvas-zoom-value]');
 const setZoom=value=>{zoom=Math.max(.5,Math.min(1.6,Math.round(value*20)/20));canvas.style.zoom=zoom;if(zoomValue)zoomValue.textContent=`${Math.round(zoom*100)}%`;draw();};
 toolbar?.querySelectorAll('[data-canvas-zoom]').forEach(button=>button.addEventListener('click',()=>setZoom(zoom+Number(button.dataset.canvasZoom))));
 toolbar?.querySelector('[data-reset-canvas]')?.addEventListener('click',()=>{setZoom(1);canvas.scrollTo({left:0,top:0,behavior:'smooth'});notify('Ansicht zurückgesetzt')});
 const svg=document.createElementNS('http://www.w3.org/2000/svg','svg');svg.classList.add('workflow-lines');svg.setAttribute('role','group');svg.setAttribute('aria-label','Workflow-Verbindungen');svg.innerHTML='<defs></defs>';canvas.prepend(svg);
 // CSS zoom scales getBoundingClientRect(), while SVG coordinates and scroll
 // positions remain in the canvas' layout coordinate system. Convert visual
 // coordinates back once here so edges stay attached at every zoom level.
 const point=(node,side)=>({x:node.offsetLeft+(side==='right'?node.offsetWidth:0),y:node.offsetTop+node.offsetHeight/2});
 const pointerPoint=event=>{const c=canvas.getBoundingClientRect();return{x:(event.clientX-c.left)/zoom+canvas.scrollLeft,y:(event.clientY-c.top)/zoom+canvas.scrollTop}};
 const edgeStyles=[['var(--edge-1)','solid'],['var(--edge-2)','dashed'],['var(--edge-3)','dotted'],['var(--edge-4)','solid']];
 const path=(a,b,lane=0)=>{const start={x:a.x,y:a.y},end={x:b.x,y:b.y},mid=(start.x+end.x)/2+lane*18;return `M ${start.x} ${start.y} H ${mid-12} Q ${mid} ${start.y} ${mid} ${start.y+12} V ${end.y-12} Q ${mid} ${end.y} ${mid+12} ${end.y} H ${end.x}`};
 const draw=()=>{const width=Math.max(canvas.clientWidth,canvas.scrollWidth),height=Math.max(canvas.clientHeight,canvas.scrollHeight);svg.setAttribute('width',width);svg.setAttribute('height',height);svg.setAttribute('viewBox',`0 0 ${width} ${height}`);svg.querySelectorAll('.edge-path,.workflow-label').forEach(x=>x.remove());const defs=svg.querySelector('defs');defs.replaceChildren();const edges=[...canvas.querySelectorAll('.workflow-edge')];const pairCounts=new Map;edges.forEach(edge=>{const key=`${edge.dataset.from}:${edge.dataset.to}`;pairCounts.set(key,(pairCounts.get(key)||0)+1)});const pairIndexes=new Map;edges.forEach((edge,index)=>{const from=canvas.querySelector(`[data-column-id="${edge.dataset.from}"]`),to=canvas.querySelector(`[data-column-id="${edge.dataset.to}"]`);if(!from||!to)return;const key=`${edge.dataset.from}:${edge.dataset.to}`,count=pairCounts.get(key)||1,position=pairIndexes.get(key)||0;pairIndexes.set(key,position+1);const lane=(position-(count-1)/2),[color,style]=edgeStyles[index%edgeStyles.length];const markerId=`flow-arrow-${index}`;const marker=document.createElementNS(svg.namespaceURI,'marker');marker.id=markerId;marker.setAttribute('markerWidth','8');marker.setAttribute('markerHeight','8');marker.setAttribute('refX','7');marker.setAttribute('refY','4');marker.setAttribute('orient','auto');const arrow=document.createElementNS(svg.namespaceURI,'path');arrow.setAttribute('d','M0,0 L8,4 L0,8z');arrow.setAttribute('fill',color);marker.append(arrow);defs.append(marker);const p=document.createElementNS(svg.namespaceURI,'path');p.classList.add('edge-path',`edge-${style}`);p.style.setProperty('--edge-color',color);p.setAttribute('d',path(point(from,'right'),point(to,'left'),lane));p.setAttribute('marker-end',`url(#${markerId})`);p.setAttribute('aria-label',`${from.querySelector('.node-body strong').textContent} → ${to.querySelector('.node-body strong').textContent}`);svg.append(p);if(edge.dataset.label){const label=document.createElementNS(svg.namespaceURI,'text');label.classList.add('workflow-label');const a=point(from,'right'),b=point(to,'left');label.setAttribute('x',(a.x+b.x)/2);label.setAttribute('y',(a.y+b.y)/2-7+lane*4);label.setAttribute('text-anchor','middle');label.textContent=edge.dataset.label;svg.append(label)}})};
 toolbar?.querySelector('[data-fit-canvas]')?.addEventListener('click',()=>{const nodes=[...canvas.querySelectorAll('.flow-node')];if(!nodes.length)return;const maxX=Math.max(...nodes.map(node=>node.offsetLeft+node.offsetWidth))+80,maxY=Math.max(...nodes.map(node=>node.offsetTop+node.offsetHeight))+80;const fit=Math.min(canvas.clientWidth/Math.max(maxX,1),canvas.clientHeight/Math.max(maxY,1),1);setZoom(Math.max(.5,fit));canvas.scrollTo({left:0,top:0,behavior:'smooth'});notify('Inhalt angepasst')});
 const fullscreenButton=toolbar?.querySelector('[data-toggle-canvas-fullscreen]');fullscreenButton?.addEventListener('click',()=>{const workspace=canvas.closest('.flow-workspace');const active=workspace?.classList.toggle('is-maximized')||false;fullscreenButton.setAttribute('aria-pressed',String(active));fullscreenButton.textContent=active?'Maximierung verlassen':'Canvas maximieren';document.body.classList.toggle('canvas-is-maximized',active);requestAnimationFrame(draw)});
 const pan={active:false,x:0,y:0,left:0,top:0};canvas.addEventListener('pointerdown',event=>{if(event.button!==1&&!event.altKey&&!event.shiftKey)return;if(event.target.closest('.flow-node'))return;pan.active=true;pan.x=event.clientX;pan.y=event.clientY;pan.left=canvas.scrollLeft;pan.top=canvas.scrollTop;canvas.classList.add('is-panning');canvas.setPointerCapture?.(event.pointerId);event.preventDefault()});canvas.addEventListener('pointermove',event=>{if(!pan.active)return;canvas.scrollLeft=pan.left-(event.clientX-pan.x)/zoom;canvas.scrollTop=pan.top-(event.clientY-pan.y)/zoom});canvas.addEventListener('pointerup',()=>{pan.active=false;canvas.classList.remove('is-panning')});
 canvas.addEventListener('wheel',event=>{if(!event.ctrlKey)return;event.preventDefault();setZoom(zoom+(event.deltaY<0?.1:-.1))},{passive:false});
 canvas.addEventListener('keydown',event=>{const distance=event.shiftKey?120:40;if(!['ArrowUp','ArrowDown','ArrowLeft','ArrowRight','+','-','='].includes(event.key))return;event.preventDefault();if(event.key==='='||event.key==='+')setZoom(zoom+.1);else if(event.key==='-')setZoom(zoom-.1);else canvas.scrollBy({left:event.key==='ArrowRight'?distance:event.key==='ArrowLeft'?-distance:0,top:event.key==='ArrowDown'?distance:event.key==='ArrowUp'?-distance:0})});
 let moveNode,moveOrigin;
 canvas.querySelectorAll('.node-grip').forEach(grip=>grip.addEventListener('pointerdown',event=>{event.preventDefault();moveNode=grip.closest('.flow-node');moveOrigin={x:event.clientX,y:event.clientY,left:moveNode.offsetLeft,top:moveNode.offsetTop};grip.setPointerCapture(event.pointerId)}));
 window.addEventListener('pointermove',event=>{if(!moveNode||!moveOrigin)return;const x=Math.max(0,moveOrigin.left+(event.clientX-moveOrigin.x)/zoom),y=Math.max(0,moveOrigin.top+(event.clientY-moveOrigin.y)/zoom);moveNode.style.left=`${x}px`;moveNode.style.top=`${y}px`;moveNode.dataset.x=Math.round(x);moveNode.dataset.y=Math.round(y);draw()});
 window.addEventListener('pointerup',()=>{if(!moveNode)return;const node=moveNode;moveNode=null;moveOrigin=null;fetch(`/columns/${node.dataset.columnId}/position`,{method:'POST',headers:csrfHeaders({'Content-Type':'application/x-www-form-urlencoded'}),body:new URLSearchParams({x:node.dataset.x,y:node.dataset.y})}).catch(()=>{});});
 let connecting,temporary;
 canvas.querySelectorAll('.node-handle').forEach(handle=>handle.addEventListener('pointerdown',event=>{event.preventDefault();event.stopPropagation();connecting=handle.closest('.flow-node');const start=point(connecting,'right');temporary=document.createElementNS(svg.namespaceURI,'path');temporary.classList.add('temporary');temporary.setAttribute('d',path(start,pointerPoint(event)));svg.append(temporary);handle.setPointerCapture(event.pointerId)}));
 window.addEventListener('pointermove',event=>{if(!connecting)return;temporary.setAttribute('d',path(point(connecting,'right'),pointerPoint(event)))});
 window.addEventListener('pointerup',event=>{if(!connecting)return;const target=document.elementFromPoint(event.clientX,event.clientY)?.closest('.flow-node');if(target&&target!==connecting){const modal=document.getElementById('new-transition');modal.querySelector('[name=from]').value=connecting.dataset.columnId;modal.querySelector('[name=to]').value=target.dataset.columnId;modal.querySelector('[data-transition-from]').textContent=connecting.querySelector('.node-body strong').textContent;modal.querySelector('[data-transition-to]').textContent=target.querySelector('.node-body strong').textContent;openModal(modal)}temporary?.remove();temporary=null;connecting=null});
 draw();window.addEventListener('resize',draw);canvas.addEventListener('scroll',draw);
 const enhanceEdgeAccessibility=()=>svg.querySelectorAll('.edge-path').forEach(edge=>{edge.setAttribute('role','img');edge.setAttribute('tabindex','0');edge.setAttribute('focusable','true');if(!edge.querySelector('title')){const title=document.createElementNS(svg.namespaceURI,'title');title.textContent=edge.getAttribute('aria-label')||'Workflow-Verbindung';edge.prepend(title)}});
 const edgeObserver=new MutationObserver(enhanceEdgeAccessibility);edgeObserver.observe(svg,{childList:true});enhanceEdgeAccessibility();
}

const panel=document.getElementById('task-panel');
async function openTaskPanel(url){if(!panel)return;const response=await fetch(`${url}/panel`);if(!response.ok)return;panel.innerHTML=await response.text();panel.classList.add('open');}
document.addEventListener('click',event=>{const link=event.target.closest('.task-card a');if(!link||!panel)return;event.preventDefault();openTaskPanel(link.href);if(history.pushState)history.pushState({},'',link.href);});
document.addEventListener('keydown',event=>{const card=event.target.closest?.('.task-card');if(!card||event.target.closest('a,button,input,textarea,select'))return;if(event.key==='Enter'||event.key===' '){event.preventDefault();const link=card.querySelector('a');if(link){openTaskPanel(link.href);link.focus()}}});
document.addEventListener('click',event=>{if(event.target.closest('[data-close-panel]')){panel?.classList.remove('open');if(panel)panel.innerHTML='';}});
document.addEventListener('submit',async event=>{const form=event.target.closest('[data-comment-form],[data-panel-form]');if(!form||!panel)return;event.preventDefault();const response=await fetch(form.action,{method:'POST',headers:csrfHeaders({'X-Task-Panel':'true'}),body:new FormData(form)});if(response.ok){openTaskPanel(form.action.replace(/\/(comments|move)$/,''));notify(form.matches('[data-comment-form]')?'Kommentar gespeichert':'Aufgabe verschoben')}else notify('Änderung konnte nicht gespeichert werden','error')});
const taskTabs=document.querySelector('[role="tablist"]');
if(taskTabs){
 const tabs=[...taskTabs.querySelectorAll('[data-task-tab]')],panels=[...document.querySelectorAll('[data-task-tab-panel]')];
 const selectTaskTab=(name,writeHistory=true,focus=false)=>{const active=tabs.some(tab=>tab.dataset.taskTab===name)?name:'conversation';tabs.forEach((tab,index)=>{const selected=tab.dataset.taskTab===active;tab.setAttribute('aria-selected',selected);tab.tabIndex=selected?0:-1;if(selected&&focus)tab.focus()});panels.forEach(panel=>{panel.hidden=panel.dataset.taskTabPanel!==active});if(writeHistory){const url=new URL(location.href);url.searchParams.set('tab',active);history.pushState({taskTab:active},'',url)}};
 const initial=new URL(location.href).searchParams.get('tab');selectTaskTab(initial||'conversation',false);
 tabs.forEach((tab,index)=>{tab.addEventListener('click',event=>{event.preventDefault();selectTaskTab(tab.dataset.taskTab,true,true)});tab.addEventListener('keydown',event=>{if(!['ArrowRight','ArrowDown','ArrowLeft','ArrowUp','Home','End'].includes(event.key))return;event.preventDefault();let next=index;if(event.key==='ArrowRight'||event.key==='ArrowDown')next=(index+1)%tabs.length;if(event.key==='ArrowLeft'||event.key==='ArrowUp')next=(index+tabs.length-1)%tabs.length;if(event.key==='Home')next=0;if(event.key==='End')next=tabs.length-1;selectTaskTab(tabs[next].dataset.taskTab,true,true)})});
 addEventListener('popstate',event=>selectTaskTab(event.state?.taskTab||new URL(location.href).searchParams.get('tab')||'conversation',false));
}

// Board filters are progressive enhancement over the server-rendered kanban.
// The board id is part of the storage key so a filter can never leak between boards.
const boardFilter=document.querySelector('[data-board-filter]');
if(boardFilter){
 const boardID=boardFilter.dataset.boardFilter, storageKey=`shipyard.board-filters.${boardID}`;
 const search=boardFilter.querySelector('[data-filter-search]'), controls=[...boardFilter.querySelectorAll('[data-filter]')];
 const cards=[...document.querySelectorAll('.kanban .task-card')];
 const count=boardFilter.querySelector('[data-filter-count]'), empty=document.querySelector('[data-filter-empty]'), chips=boardFilter.querySelector('[data-filter-chips]');
 const state={q:'',column:'',priority:'',label:''};
 try{Object.assign(state,JSON.parse(sessionStorage.getItem(storageKey)||'{}'))}catch(_){ }
 search.value=state.q;controls.forEach(control=>control.value=state[control.dataset.filter]||'');
 const labels=new Map(controls.find(control=>control.dataset.filter==='label')?.options? [...controls.find(control=>control.dataset.filter==='label').options].map(option=>[option.value,option.textContent]):[]);
 const columns=new Map(controls.find(control=>control.dataset.filter==='column')?.options? [...controls.find(control=>control.dataset.filter==='column').options].map(option=>[option.value,option.textContent]):[]);
 const priorities=new Map([['urgent','Dringend'],['high','Hoch'],['normal','Normal'],['low','Niedrig']]);
 const save=()=>{try{sessionStorage.setItem(storageKey,JSON.stringify(state))}catch(_){}};
 const render=()=>{const query=state.q.trim().toLocaleLowerCase();let visible=0;const perColumn=new Map();cards.forEach(card=>{const haystack=`${card.dataset.title||''} ${card.dataset.description||''}`.toLocaleLowerCase();const matches=(!query||haystack.includes(query))&&(!state.column||card.dataset.column===state.column)&&(!state.priority||card.dataset.priority===state.priority)&&(!state.label||(` ${card.dataset.labels||''} `).includes(` ${state.label} `));card.hidden=!matches;if(matches){visible++;perColumn.set(card.dataset.column,(perColumn.get(card.dataset.column)||0)+1)}});document.querySelectorAll('[data-column]').forEach(column=>{const total=perColumn.get(column.dataset.column)||0;const value=column.querySelector('[data-column-count]');if(value)value.textContent=String(total)});count.textContent=`${visible} ${visible===1?'Aufgabe':'Aufgaben'}`;if(empty)empty.hidden=visible!==0;chips.replaceChildren();const active=[['q',state.q,'Suche'],['column',state.column,columns.get(state.column)],['priority',state.priority,priorities.get(state.priority)],['label',state.label,labels.get(state.label)]];active.filter(([,value])=>value).forEach(([key,value,name])=>{const chip=document.createElement('button');chip.type='button';chip.className='active-filter';chip.dataset.clearFilter=key;chip.textContent=`${name||key}: ${value} ×`;chips.append(chip)});save()};
 search.addEventListener('input',()=>{state.q=search.value;clearTimeout(search._filterTimer);search._filterTimer=setTimeout(render,180)});
 controls.forEach(control=>control.addEventListener('change',()=>{state[control.dataset.filter]=control.value;render()}));
 boardFilter.querySelector('[data-filter-reset]')?.addEventListener('click',()=>{state.q='';search.value='';controls.forEach(control=>{state[control.dataset.filter]='';control.value=''});render();search.focus()});
 chips.addEventListener('click',event=>{const button=event.target.closest('[data-clear-filter]');if(!button)return;const key=button.dataset.clearFilter;state[key]='';if(key==='q')search.value='';else boardFilter.querySelector(`[data-filter="${CSS.escape(key)}"]`).value='';render()});
 render();
}

// Keep the native select as the submitted value, while presenting searchable options.
function searchResource(sel){if(sel.matches('.doc-kind'))return '';if(sel.matches('.brands'))return 'brands';if(sel.matches('.categories'))return 'categories';if(sel.matches('.items'))return 'items';if(sel.matches('.vendors'))return 'vendors';if(sel.matches('.customers'))return 'customers';if(sel.id==='approvedRequests')return 'requests';if(sel.id==='openOrders')return 'orders';if(sel.matches('.doc-reference'))return 'documents';if(sel.name==='party')return sel.closest('#transactionEdit')||sel.form?.elements.kind?.value==='PO'?'vendors':'customers';if(sel.matches('.line-item'))return ['PO','GR'].includes(sel.form?.elements.kind.value)?'lines':'items';return ''}

// One shared popup avoids clipping by scrollable cards and avoids orphaned lists
// when a document line is removed or the table is redrawn.
const searchPopup=document.createElement('div');
searchPopup.className='search-results search-popup';
searchPopup.hidden=true;
searchPopup.id='shared-search-options';
searchPopup.setAttribute('role','listbox');
const supportsSearchPopover=typeof searchPopup.showPopover==='function';
if(supportsSearchPopover)searchPopup.setAttribute('popover','manual');
document.body.append(searchPopup);
let activeSearch=null,searchRequest=0;

function positionSearchPopup(){
 if(!activeSearch||searchPopup.hidden)return;
 const {input}=activeSearch,rect=input.getBoundingClientRect();
 if(!input.isConnected||rect.bottom<0||rect.top>window.innerHeight||rect.right<0||rect.left>window.innerWidth){closeSearchPopup();return}
 const margin=8,gap=4,dialog=!supportsSearchPopover?activeSearch.sel.closest('dialog[open]'):null;
 const bounds=dialog?.getBoundingClientRect();
 const topLimit=bounds?Math.max(margin,bounds.top+margin):margin;
 const bottomLimit=bounds?Math.min(window.innerHeight-margin,bounds.bottom-margin):window.innerHeight-margin;
 const below=bottomLimit-rect.bottom-gap,above=rect.top-topLimit-gap;
 const openUp=below<180&&above>below,available=Math.max(40,openUp?above:below);
 searchPopup.style.left=Math.max(margin,Math.min(rect.left,window.innerWidth-Math.min(rect.width,window.innerWidth-2*margin)-margin))+'px';
 searchPopup.style.width=Math.min(rect.width,window.innerWidth-2*margin)+'px';
 searchPopup.style.maxHeight=Math.min(300,available)+'px';
 searchPopup.style.top=openUp?'auto':(rect.bottom+gap)+'px';
 searchPopup.style.bottom=openUp?(window.innerHeight-rect.top+gap)+'px':'auto';
}
function closeSearchPopup(){
 searchRequest++;
 if(activeSearch)activeSearch.input.setAttribute('aria-expanded','false');
 if(supportsSearchPopover&&searchPopup.matches(':popover-open'))searchPopup.hidePopover();
 searchPopup.hidden=true;activeSearch=null;
}
window.addEventListener('resize',positionSearchPopup);
document.addEventListener('scroll',event=>{if(event.target===searchPopup||searchPopup.contains(event.target))return;positionSearchPopup()},true);
document.addEventListener('pointerdown',event=>{if(activeSearch&&!activeSearch.wrap.contains(event.target)&&!searchPopup.contains(event.target))closeSearchPopup()});
document.addEventListener('focusin',event=>{if(activeSearch&&!activeSearch.wrap.contains(event.target)&&!searchPopup.contains(event.target))closeSearchPopup()});

function enhanceSelect(sel){
 if(sel.dataset.searchReady)return;
 sel.dataset.searchReady='1';
 const wrap=document.createElement('div');wrap.className='search-select';sel.before(wrap);wrap.append(sel);
 const input=document.createElement('input');input.type='search';input.autocomplete='off';input.placeholder='Type to search…';input.setAttribute('aria-label',sel.closest('label')?.childNodes[0]?.textContent?.trim()||'Search options');input.setAttribute('role','combobox');input.setAttribute('aria-haspopup','listbox');input.setAttribute('aria-expanded','false');input.setAttribute('aria-controls',searchPopup.id);input.required=sel.required;sel.required=false;sel.classList.add('search-native');wrap.append(input);
 let timer,suppressFocus=false;
 function sync(){const opt=sel.selectedOptions[0];input.value=opt?.value?opt.textContent:'';input.required=sel.dataset.required==='true'||(sel.dataset.required===undefined&&input.required)}
 sel.addEventListener('change',sync);sel.form?.addEventListener('reset',()=>setTimeout(sync,0));
 async function show(){
  if(activeSearch&&activeSearch.input!==input)closeSearchPopup();
  // A focused field with an existing selection should show all choices until
  // the user types a search, rather than filtering to its current value.
  const selectedText=sel.selectedOptions[0]?.textContent?.trim();
  const q=sel.value&&input.value.trim()===selectedText?'':input.value.trim();
  const current=++searchRequest,resource=searchResource(sel);activeSearch={input,sel,wrap};let allowed=null;
  if(resource){try{const result=await api('search?resource='+encodeURIComponent(resource)+'&q='+encodeURIComponent(q));if(current!==searchRequest||activeSearch?.input!==input)return;allowed=new Set(result.map(r=>String(r.value)))}catch(e){if(current!==searchRequest||activeSearch?.input!==input)return;searchPopup.textContent='Search unavailable';openPopup();return}}
  if(current!==searchRequest||activeSearch?.input!==input)return;
  const matches=[...sel.options].filter(o=>o.value&&(!allowed||allowed.has(o.value))&&o.textContent.toLocaleLowerCase().includes(q.toLocaleLowerCase())).slice(0,100);
  searchPopup.replaceChildren();
  for(const option of matches){const b=document.createElement('button');b.type='button';b.setAttribute('role','option');b.textContent=option.textContent;b.onmousedown=e=>e.preventDefault();b.onclick=()=>{clearTimeout(timer);input.setCustomValidity('');sel.value=option.value;closeSearchPopup();sel.dispatchEvent(new Event('change',{bubbles:true}))};searchPopup.append(b)}
  if(!matches.length){const empty=document.createElement('div');empty.className='search-empty';empty.textContent='No matches';searchPopup.append(empty)}
  openPopup();
 }
 function openPopup(){
  // Keep a dialog's options inside it for focus/inertness. A native popover
  // escapes clipping; older browsers position the menu within the dialog.
  const host=sel.closest('dialog[open]')||document.body;if(searchPopup.parentElement!==host)host.append(searchPopup);
  searchPopup.hidden=false;if(supportsSearchPopover&&!searchPopup.matches(':popover-open'))searchPopup.showPopover();
  input.setAttribute('aria-expanded','true');positionSearchPopup();
 }
 input.addEventListener('input',()=>{sel.value='';input.setCustomValidity(input.value?'Choose a matching option.':'');clearTimeout(timer);timer=setTimeout(show,180)});
 input.addEventListener('focus',()=>{if(suppressFocus){suppressFocus=false;return}input.select();show()});
 input.addEventListener('keydown',e=>{if(e.key==='Escape'){closeSearchPopup();sync()}if(e.key==='ArrowDown'&&activeSearch?.input===input&&!searchPopup.hidden){e.preventDefault();searchPopup.querySelector('button')?.focus()}});
 input.addEventListener('blur',()=>setTimeout(()=>{if(!wrap.contains(document.activeElement)&&!searchPopup.contains(document.activeElement)){if(activeSearch?.input===input)closeSearchPopup();if(!sel.value){input.value='';input.setCustomValidity('')}}},150));
 new MutationObserver(()=>{if(document.activeElement!==input)sync()}).observe(sel,{childList:true,subtree:true});sync();
 // Used by the shared popup to restore focus without immediately reopening it.
 input.restoreSearchFocus=()=>{suppressFocus=true;input.focus()};
}
searchPopup.addEventListener('keydown',e=>{const buttons=[...searchPopup.querySelectorAll('button')],i=buttons.indexOf(document.activeElement);if(e.key==='ArrowDown'||e.key==='ArrowUp'){e.preventDefault();buttons[Math.max(0,Math.min(buttons.length-1,i+(e.key==='ArrowDown'?1:-1)))]?.focus()}if(e.key==='Escape'){e.preventDefault();const input=activeSearch?.input;closeSearchPopup();input?.restoreSearchFocus()}});
function enhanceAllSelects(){document.querySelectorAll('select:not([data-search-ready])').forEach(enhanceSelect)}
let searchScanPending=false;new MutationObserver(()=>{if(activeSearch&&!activeSearch.wrap.isConnected)closeSearchPopup();if(!searchScanPending){searchScanPending=true;queueMicrotask(()=>{searchScanPending=false;enhanceAllSelects()})}}).observe(document.body,{childList:true,subtree:true});enhanceAllSelects();

(async()=>{
 try{
  const config=await api('config');if(!config.demo)return;
  $('#demoBanner').hidden=false;document.querySelector('.header-right').innerHTML='<span class="dot"></span> Portfolio demo <span class="avatar">S</span>';
  const selectors=['.doc-submit','#saveItem','#recalculate','#captureMonth','#prepareRequest','[data-doc-decision]','[data-unpost-legacy]','[data-edit-legacy]','[data-post-legacy]','[data-unpost-document]','[data-edit-document]','[data-post-document]','[data-edit-item]','[data-decision]','#brandForm button','#categoryForm button','#customerForm button[type=submit]','#vendorForm button[type=submit]','#customerForm button:not([type])','#vendorForm button:not([type])','#requestForm button','#orderForm button','#receiptForm button','#issueForm button','#saleForm button','[data-edit-customer]','[data-edit-vendor]'];
  if(!config.ai_enabled)selectors.push('#explainDaily','#explainRestock');
  const disable=()=>document.querySelectorAll(selectors.join(',')).forEach(el=>{el.disabled=true;el.title='Unavailable in the read-only portfolio demo.'});
  disable();new MutationObserver(disable).observe(document.body,{childList:true,subtree:true});
  if(!config.ai_enabled){const el=document.createElement('p');el.className='ledger-help';el.textContent='AI generation is disabled in this public demo; calculated recommendations remain available.';$('#dailyFacts').before(el)}
 }catch(e){console.error('Could not load app configuration:',e)}
})();

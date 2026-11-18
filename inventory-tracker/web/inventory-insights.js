let insightData=null;
async function refreshInsights(){
 const result=await api('inventory-insights');insightData=result;
 const rows=result.recommendations;
 table('#restockTable',[
  ['Select',r=>`<input type="checkbox" class="insight-select" aria-label="Select ${esc(r.name)}" value="${r.id}">`],
  ['SKU',field('sku')],['Item',field('name')],['On hand',r=>num(r.on_hand)+' '+esc(r.unit)],
  ['Reorder level',field('reorder_level',num)],['Open orders',field('incoming',num)],['Pending requests',field('pending_request',num)],
  ['Projected',field('projected_stock',num)],['Used in 30 days',field('usage_30_days',num)],
  ['Suggested',r=>num(r.suggested_quantity)+' '+esc(r.unit)],['Est. unit cost',field('unit_cost',money)],
  ['Data',r=>r.limited_history?'No recent usage':'Recent usage']
 ],rows);
 if(!rows.length)$('#restockTable').innerHTML='<div class="empty">No items currently need replenishment using these rules.</div>';
 const d=result.daily;$('#dailyFacts').innerHTML=`<div class="insight-facts">${[['Date',d.day],['Receipt lines',d.receipts],['Issue lines',d.issues],['Sale lines',d.sales],['Revenue',money(d.revenue)],['Sales COGS',money(d.cogs)],['Low-stock items',d.low_stock_items],['Pending request lines',d.pending_request_lines],['Stock value',money(d.stock_value)]].map(([label,value])=>`<div><small>${esc(label)}</small><strong>${esc(value)}</strong></div>`).join('')}</div>`;
 for(const id of ['#restockExplanation','#dailyExplanation']){$(id).hidden=true;$(id).textContent=''}
}
async function generateInsight(kind,button,target){
 button.disabled=true;const output=$(target);output.hidden=false;output.textContent='Generating with the local model…';
 try{const response=await api('inventory-insights/explain',{kind});output.textContent=response.text+'\n\nGenerated locally with '+response.model+'.'}catch(e){output.textContent=e.message}finally{button.disabled=false}
}
document.querySelector('[data-page="insights"]').addEventListener('click',()=>refreshInsights().catch(e=>toast(e.message)));
$('#refreshInsights').onclick=()=>refreshInsights().catch(e=>toast(e.message));
$('#explainRestock').onclick=e=>generateInsight('restock',e.currentTarget,'#restockExplanation');
$('#explainDaily').onclick=e=>generateInsight('daily',e.currentTarget,'#dailyExplanation');
$('#prepareRequest').onclick=()=>{
 if(!insightData){toast('Refresh recommendations first.');return}
 const ids=[...document.querySelectorAll('.insight-select:checked')].map(x=>Number(x.value)), selected=insightData.recommendations.filter(x=>ids.includes(Number(x.id)));
 if(!selected.length){toast('Select at least one item.');return}if(selected.length>50){toast('Select at most 50 items per request.');return}
 const form=$('#purchaseDocument');const hasDraft=form.elements.note.value.trim()||[...form.querySelectorAll('.line-quantity,.line-price')].some(x=>x.value)||[...form.querySelectorAll('.line-item')].some(x=>x.value);
 if(hasDraft&&!confirm('Replace the current purchasing document draft with these recommendations?'))return;
 form.reset();form.elements.kind.value='PR';form.elements.kind.dispatchEvent(new Event('change',{bubbles:true}));form.elements.note.value='Inventory replenishment';form.querySelector('.line-body').innerHTML='';
 for(const item of selected){addLine(form);const row=form.querySelector('.line-body tr:last-child');const sel=row.querySelector('.line-item');sel.value=String(item.id);sel.dispatchEvent(new Event('change',{bubbles:true}));row.querySelector('.line-quantity').value=item.suggested_quantity;row.querySelector('.line-price').value=item.unit_cost>0?item.unit_cost:''}updateTotal(form);page('purchasing');form.scrollIntoView({behavior:'smooth',block:'start'});toast('Review the draft and enter any missing estimated costs before posting.');
};

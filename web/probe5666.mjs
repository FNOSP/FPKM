import { chromium } from 'playwright';
const b = await chromium.launch({headless:true});
const ctx = await b.newContext({viewport:{width:1280,height:800}});
const page = await ctx.newPage();

// 记录所有发往 /app/Lucky 的请求头
const seen = [];
page.on('request', r => {
  if (r.url().includes('/app/Lucky')) {
    seen.push({url:r.url(), headers: r.headers()});
  }
});

await page.goto('http://127.0.0.1:5666/app/Lucky/', {waitUntil:'domcontentloaded', timeout:20000}).catch(e=>console.log('  goto err:', e.message.slice(0,60)));
await page.waitForTimeout(2500);
const body = await page.evaluate(()=>document.body?.innerText?.slice(0,200) || '');
console.log('  页面内容:', JSON.stringify(body));
console.log('  请求数:', seen.length);
seen.slice(0,3).forEach((s,i)=>{
  console.log(`  --- 请求 ${i+1}: ${s.url.slice(0,70)}`);
  console.log('      头:', JSON.stringify(s.headers).slice(0,400));
});
await b.close();

export async function fetchJSON(url: string): Promise<any> {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`bad status ${res.status}`);
  return res.json();
}
var cache = {};

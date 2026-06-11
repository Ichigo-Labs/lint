interface Config { url?: string; token?: string }
export function setup(c: Config) {
  const url = c.url!;
  const token = c.token!.trim();
  console.info("setup", url, token);
  return url;
}

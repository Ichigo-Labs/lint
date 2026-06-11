abstract class Repository<T extends { id: string }> {
  private cache = new Map<string, T>();
  constructor(protected readonly name: string) {}
  abstract fetch(id: string): Promise<T>;
  async get(id: string): Promise<T> {
    const hit = this.cache.get(id);
    if (hit !== undefined) return hit;
    const value = await this.fetch(id);
    this.cache.set(id, value);
    return value;
  }
}
class UserRepo extends Repository<{ id: string; email: string }> {
  async fetch(id: string) {
    console.debug("fetching", id);
    return { id, email: `user-${id}@example.com` };
  }
}

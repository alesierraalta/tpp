export class DashboardLoader {
  constructor(client) {
    this.client = client;
  }

  // The three panels, gathered in one pass.
  async load() {
    const [profile, orders, activity] = await Promise.all([
      this.client.profile(),
      this.client.orders(),
      this.client.activity(),
    ]);
    return { profile, orders, activity };
  }
}

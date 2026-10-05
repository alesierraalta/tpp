export class DashboardLoader {
  constructor(client) {
    this.client = client;
  }

  // The three panels, gathered in one pass.
  async load() {
    const profile = await this.client.profile();
    const orders = await this.client.orders();
    const activity = await this.client.activity();
    return { profile, orders, activity };
  }
}

// Only the Node unit tests use this constructor; Miniflare loads the real runtime module.
export class DurableObject {
  constructor(
    protected ctx: unknown,
    protected env: unknown,
  ) {}
}

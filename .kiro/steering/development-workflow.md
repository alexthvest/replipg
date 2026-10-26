# Incremental Delivery

- Implement specifications sequentially unless explicitly approved otherwise.
- Organize implementation into runnable vertical milestones.
- Every milestone must end with:
  - automated tests,
  - a user-visible demonstration in a Telegram test group,
  - documented commands for reproducing the result.
- Do not postpone Telegram integration until the final task.
- Use deterministic clocks and randomness in tests.
- Provide development-only mechanisms for behavior that otherwise depends on time or randomness.

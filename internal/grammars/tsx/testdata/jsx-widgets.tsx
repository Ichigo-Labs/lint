export function Widgets({ items }: { items: string[] }) {
  const openDialog = () => console.log("open");
  return (
    <section>
      <Button click={() => save()} open={openDialog} onClose={closeDialog}>
        Save
      </Button>
      <List items={items} render={(it: string) => <li key={it}>{it}</li>} />
      <Card style={{ margin: 0 }} title="hi" />
    </section>
  );
}

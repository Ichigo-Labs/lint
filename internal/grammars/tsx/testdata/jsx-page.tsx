const Page = () => (
  <main>
    {flag == null ? <Spinner /> : flag ? <Body /> : <Empty />}
    <Hero select={selectPlan} layout={{ wide: true }} />
  </main>
);

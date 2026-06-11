export const Form = () => {
  var draft = {};
  return (
    <form submit={function (e) { e.preventDefault(); }}>
      <Input change={setValue} value={value!} />
      <Select toggle={toggleOpen} options={opts} config={{ multi: true }} />
      <button onClick={() => console.log("clicked")}>Go</button>
    </form>
  );
};

self.onmessage = (e) => {
  if (e.data == "ping") {
    console.log("pong", e.data, Date.now());
  }
};

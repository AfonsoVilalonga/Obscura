const express = require('express');
const path = require('path');
const app = express();
const port = 3010;

app.use(express.static(path.join(__dirname)));
app.use(express.static("output.ivf"));
app.use(express.static(path.join(__dirname, 'html')));
app.use(express.static(path.join(__dirname, 'videos')));

app.get('/', (req, res) => {
  res.sendFile(path.join(__dirname, 'html/index.html'));
});

app.listen(port, () => {
  console.log(`Server is running at http://localhost:${port}`);
});
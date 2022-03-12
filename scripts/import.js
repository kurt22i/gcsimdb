var fs = require("fs");
var Papa = require("papaparse");
const fetch = require("sync-fetch");
const pako = require("pako");
const yaml = require("js-yaml");

function extractJSONStringFromBinary(binaryStr) {
  try {
    const restored = pako.inflate(binaryStr, { to: "string" });
    return {
      err: "",
      data: restored,
    };
  } catch {
    return {
      err: "Not a valid gzipped JSON file",
      data: "",
    };
  }
}

function Uint8ArrayFromBase64(base64) {
  return Uint8Array.from(atob(base64), (v) => v.charCodeAt(0));
}

const s = fs.readFileSync("./kurt-sheet.csv", { encoding: "utf8" });

const result = Papa.parse(s);

// console.log(result);

result.data.forEach((d, i) => {
  //   if (i > 0) {
  //     return;
  //   }
  //4 = url
  //5 = file name
  //6 = author
  //7 = desc
  //grab data from url
  let viewer_link = d[4];
  //https://gcsim.app/viewer/share/perm_AY1MdsFpZipK4qxYP4cLn
  let url = viewer_link.replace(
    "https://gcsim.app/viewer/share/",
    "https://viewer.gcsim.workers.dev/"
  );

  let x = fetch(url).json();

  const binaryStr = Buffer.from(x.data, "base64");
  let jsonData = extractJSONStringFromBinary(binaryStr);
  let data = JSON.parse(jsonData.data);

  let out = {
    author: d[6],
    description: d[7],
    config: data.config_file,
  };

  fs.writeFileSync(`./dump/${d[5]}.yaml`, yaml.dump(out));

  console.log(`done: ${d[5]}`);
});

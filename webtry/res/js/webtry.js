/**
 * Common JS that talks XHR back to the server and runs the code and receives
 * the results.
 */


/**
 * All the functionality is wrapped up in this anonymous closure, but we need
 * to be told if we are on the workspace page or a normal try page, so the
 * workspaceName is passed into the closure, it must be set in the global
 * namespace. If workspaceName is the empty string then we know we aren't
 * running on a workspace page.
 *
 * If we are on a workspace page we also look for a 'history_'
 * variable in the global namespace which contains the list of tries
 * that are included in this workspace. That variable is used to
 * populate the history list.
 */
(function() {
    function onLoad() {
      var run             = $$$('#run');
      var runText         = $$$('#run-text');
      var permalink       = $$$('#permalink');
      var embed           = $$$('#embed');
      var embedButton     = $$$('#embed-button');
      var code            = $$$('#code');
      var output          = $$$('#output');
      var outputWrapper   = $$$('#output-wrapper');
      var gpu             = $$$('#use-gpu');
      var raster          = $$$('#use-raster');
      var pdf             = $$$('#use-pdf');
      var rasterOutput    = $$$('#raster-output');
      var rasterImg       = $$$('#raster-img');
      var gpuOutput       = $$$('#gpu-output');
      var gpuImg          = $$$('#gpu-img');
      var PDFOutput       = $$$('#pdf-output');
      var PDFLink         = $$$('#pdf-link');
      var imageWidth      = $$$('#image-width');
      var imageHeight     = $$$('#image-height');
      var tryHistory      = $$$('#tryHistory');
      var parser          = new DOMParser();
      var tryTemplate     = $$$('#tryTemplate');
      var sourcesTemplate = $$$('#sourcesTemplate');
      var enableSource    = $$$('#enableSource');
      var selectedSource  = $$$('#selected-source');
      var sourceCode      = $$$('#source-code');
      var chooseSource    = $$$('#choose-source');
      var chooseList      = $$$('#choose-list');
      var bugReport       = $$$('#bugreport');

      // Id of the source image to use, 0 if no source image is used.
      var sourceId = 0;

      if (enableSource) {
        sourceId = parseInt(enableSource.getAttribute('data-id'));
        if (sourceId) {
          sourceSelectByID(sourceId);
        }
      }

      function setIFrameURL() {
        var url = document.URL;
        url = url.replace('/c/', '/iframe/');
        embed.innerText = '<iframe src="' + url + '" width="900" height="550" style="border: solid #00a 1px; border-radius: 5px;"/>'
      }

      function beginWait() {
        document.body.classList.add('waiting');
        run.disabled = true;
      }


      function endWait() {
        document.body.classList.remove('waiting');
        run.disabled = false;
      }

      function getImageWidth() {
        if (imageWidth) {
          return parseInt(imageWidth.value);
        }
        return 256;
      }

      function getImageHeight() {
        if (imageHeight) {
          return parseInt(imageHeight.value);
        }
        return 256;
      }

      function isChecked(element, defaultValue) {
        if (element) {
          return element.checked;
        }
        return defaultValue;
      }

      function sourceSelectByID(id) {
        sourceId = id;
        if (id > 0) {
          enableSource.checked = true;
          selectedSource.innerHTML = '<img with=64 height=64 src="/i/image-'+sourceId+'.png" />';
          selectedSource.classList.add('show');
          sourceCode.classList.add('show');
          chooseSource.classList.remove('show');
        } else {
          enableSource.checked = false;
          selectedSource.classList.remove('show');
          sourceCode.classList.remove('show');
        }
      }


      /**
       * A selection has been made in the choiceList.
       */
      function sourceSelect() {
        sourceSelectByID(parseInt(this.getAttribute('data-id')));
      }


      /**
       * Callback when the loading of the image sources is complete.
       *
       * Fills in the list of images from the data returned.
       */
      function sourcesComplete(e) {
        endWait();
        // The response is JSON of the form:
        // [
        //   {"id": 1},
        //   {"id": 3},
        //   ...
        // ]
        body = JSON.parse(e.target.response);
        // Clear out the old list if present.
        while (chooseList.firstChild) {
          chooseList.removeChild(chooseList.firstChild);
        }
        body.forEach(function(source) {
         var id = 'i'+source.id;
         var imgsrc = '/i/image-'+source.id+'.png';
         var clone = sourcesTemplate.content.cloneNode(true);
         clone.querySelector('img').src     = imgsrc;
         clone.querySelector('button').setAttribute('id', id);
         clone.querySelector('button').setAttribute('data-id', source.id);
         chooseList.insertBefore(clone, chooseList.firstChild);
         chooseList.querySelector('#'+id).addEventListener('click', sourceSelect, true);
        });
        chooseSource.classList.add('show');
      }


      /**
       * Toggle the use of a source image, or select a new source image.
       *
       * If enabling source images then load the list of available images via
       * XHR.
       */
      function sourceClick(e) {
        selectedSource.classList.remove('show');
        sourceCode.classList.remove('show');
        if (!enableSource.checked) {
          beginWait();
          var req = new XMLHttpRequest();
          req.addEventListener('load', sourcesComplete);
          req.addEventListener('error', xhrError);
          req.overrideMimeType('application/json');
          req.open('GET', '/sources/', true);
          req.send();
        } else {
          sourceId = 0;
          chooseSource.classList.remove('show');
        }
      }

      function configChange(e) {
        if (!(gpu.checked || raster.checked || pdf.checked)) {
          run.disabled = true;
          runText.innerText = "Choose a configuration"
        } else {
          run.disabled = false;
          runText.innerText = "Run"
        }
      }

      function setupListeners() {
        if (enableSource) {
          enableSource.addEventListener('click', sourceClick, true);
        }
        if (selectedSource) {
          selectedSource.addEventListener('click', sourceClick, true);
        }

        if (gpu) {
          gpu.addEventListener('change', configChange);
        }
        if (raster) {
          raster.addEventListener('change', configChange);
        }
        if (pdf) {
          pdf.addEventListener('change', configChange);
        }

        if (run) {
          run.addEventListener('click', onSubmitCode);
        }
      }

      setupListeners();

      if (code) {

        var editor = CodeMirror.fromTextArea(code, {
          theme: "default",
          lineNumbers: true,
          matchBrackets: true,
          lineWrapping: true,
          mode: "text/x-c++src",
          indentUnit: 4,
        });

        // Match the initial textarea width, but leave the height alone
        // The css will automatically resize the editor vertically.
        editor.setSize(editor.defaultCharWidth() * code.cols,
                       null);

        editor.on('beforeChange', function(instance, changeObj) {
          var startLine = changeObj.from.line;
          var endLine = changeObj.to.line;

          for (var i = startLine ; i <= endLine ; i++) {
            editor.removeLineClass( i, "wrap", "error" );
          }
        });
      }

      /**
       * Callback when there's an XHR error.
       * @param e The callback event.
       */
      function xhrError(e) {
        endWait();
        alert('Something bad happened: ' + e);
      }

      function clearOutput() {
        output.textContent = "";
        outputWrapper.style.display='none';
        if (embed) {
          embed.classList.remove("display");
        }
      }

      /**
       * Called when an image in the workspace history is clicked.
       */
      function historyClick() {
        beginWait();
        clearOutput();
        var req = new XMLHttpRequest();
        req.addEventListener('load', historyComplete);
        req.addEventListener('error', xhrError);
        req.overrideMimeType('application/json');
        req.open('GET', this.getAttribute('data-try'), true);
        req.send();
      }


      /**
       * Callback for when the XHR kicked off in historyClick() returns.
       */
      function historyComplete(e) {
        // The response is JSON of the form:
        // {
        //   "hash": "unique id for a try",
        //   "code": "source code for try"
        // }
        endWait();
        body = JSON.parse(e.target.response);
        code.value = body.code;
        editor.setValue(body.code);
        updateOutputImages(body)
        imageWidth.value = body.width;
        imageHeight.value = body.height;
        gpu.checked = body.gpu;
        sourceSelectByID(body.source);
        updatePermalink(body);
        updateBugReport(body);
      }


      /**
       * Add the given try image to the history of a workspace.
       */
      function addToHistory(hash, imgUrl) {
        var clone = tryTemplate.content.cloneNode(true);
        clone.querySelector('img').src = imgUrl;
        clone.querySelector('.tries').setAttribute('data-try', '/json/' + hash);
        tryHistory.insertBefore(clone, tryHistory.firstChild);
        tryHistory.querySelector('.tries').addEventListener('click', historyClick, true);
      }

      /**
       * Callback for when the user clicks on a compile error message
       *
       */

      function errorClick() {
        var line = this.getAttribute('data-line');
        var col = this.getAttribute('data-col');

        editor.setCursor(line-1,col-1);
        editor.focus();
      }

      /**
       * Take image and URL responses from the server and update the UI
       *
       */

      function updateOutputImages(body) {

        if (rasterOutput) {
          if (body.rasterImg != "") {
            rasterOutput.src = 'data:image/png;base64,' + body.rasterImg;
            rasterOutput.modTime = body.rasterMod;
            rasterOutput.classList.add("display");
          } else {
            rasterOutput.classList.remove("display");
          }
        }

        if (gpuOutput) {
          if (body.gpuImg != "") {
            gpuOutput.src = 'data:image/png;base64,' + body.gpuImg;
            gpuOutput.modTime = body.gpuMod;
            gpuOutput.classList.add("display");
          } else {
            gpuOutput.classList.remove("display");
          }
        }

        if (PDFOutput) {
          if (body.PDFURL != "") {
            PDFOutput.src = body.PDFURL;
            PDFOutput.modTime = body.pdfMod;
            PDFOutput.classList.add("display");
          } else {
            PDFOutput.classList.remove("display");
          }
        }
      }

      /**
       * Update the permalink from a json response
       */

      function updatePermalink(body) {
        if (permalink) {
          permalink.href = '/c/' + body.hash;
          permalink.style.display = 'inline-block';
        }
      }

      /**
       * Update the bug report URL and button from a json response
       */

      function updateBugReport(body) {
        if (bugreport) {
          bugreport.href = body.bugURL;
          bugreport.classList.add("display");
        }
      }



      /**
       * Callback for when the XHR returns after attempting to run the code.
       * @param e The callback event.
       */
      function codeComplete(e) {
        // The response is JSON of the form:
        // {
        //   "message": "you had an error...",
        //   "img": "<base64 encoded image but only on success>"
        // }
        //
        // The img is optional and only appears if there is a valid
        // image to display.
        endWait();
        body = JSON.parse(e.target.response);
        if (null != body.compileErrors && body.compileErrors.length) {
          html = "";
          for (i = 0 ; i < body.compileErrors.length ; i++) {
            compileError = body.compileErrors[i];

            err = document.createElement("div");
            err.className = "compile-error";

            loc = document.createElement("span");
            loc.className = "error-location";
            loc.innerHTML = "Line " + compileError.line + ", col " + compileError.column + ": ";

            errorMessage = document.createElement("span");
            errorMessage.className = "error-mesage";
            errorMessage.innerHTML = compileError.error;

            err.appendChild(loc);
            err.appendChild(errorMessage);

            err.setAttribute('data-line', compileError.line);
            err.setAttribute('data-col', compileError.column);

            output.appendChild(err);

            err.addEventListener('click', errorClick);

            editor.addLineClass( parseInt( compileError.line ) - 1, "wrap", "error" );
          }
          outputWrapper.style.display = 'block';
        } else {
          output.textContent = body.message;
          if (body.message) {
            outputWrapper.style.display = 'block';
          }
        }

        updateOutputImages(body)

        // Add the image to the history if we are on a workspace page.
        if (tryHistory) {
          addToHistory(body.hash, 'data:image/png;base64,' + body.rasterImg);
        } else {
          window.history.pushState(null, null, '/c/' + body.hash);
        }
        updatePermalink(body);
        updateBugReport(body);
        if (embed) {
          setIFrameURL();
        }
        if (embedButton) {
          embedButton.classList.add('display');
        }
      }

      function onSubmitCode() {
        beginWait();
        clearOutput();
        var req = new XMLHttpRequest();
        req.addEventListener('load', codeComplete);
        req.addEventListener('error', xhrError);
        req.overrideMimeType('application/json');
        req.open('POST', '/', true);
        req.setRequestHeader('content-type', 'application/json');
        req.send(JSON.stringify({
          'code': editor.getValue(),
          'width': getImageWidth(),
          'height': getImageHeight(),
          'name': workspaceName,
          'source': sourceId,
          'gpu': isChecked(gpu,false),
          'raster': isChecked(raster,true),
          'pdf': isChecked(pdf,false)
        }));
      }


      if (embed) {
        setIFrameURL();
      }

      // Add the images to the history if we are on a workspace page.
      if (tryHistory && history_) {
        for (var i=0; i<history_.length; i++) {
          addToHistory(history_[i].hash, '/i/'+history_[i].hash+'_raster.png');
        }
      }
    }

    // If loaded via HTML Imports then DOMContentLoaded will be long done.
    if (document.readyState != "loading") {
      onLoad();
    } else {
      this.addEventListener('load', onLoad);
    }

})();

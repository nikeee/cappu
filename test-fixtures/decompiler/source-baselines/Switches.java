class Switches {

  int dense(int arg0) {
    switch (arg0) {
      case 1:
        return 10;
      case 2:
        return 20;
      case 3:
        return 30;
    }
    return -1;
  }

  int breaks(int arg0) {
    int var2 = 0;
    switch (arg0) {
      case 0:
        var2 = 1;
        break;
      case 1:
        var2 = 2;
        break;
      default:
        var2 = 9;
    }
    return var2 + 1;
  }

  int fall(int arg0) {
    int var2 = 0;
    switch (arg0) {
      case 1:
      case 2:
        var2 = var2 + 1;
      case 3:
        var2 = var2 + 2;
        break;
      case 7:
        var2 = var2 + 4;
    }
    return var2;
  }

  int sparse(int arg0) {
    switch (arg0) {
      case 100:
        return 1;
      case 5000:
        return 2;
      case -7:
        return 3;
    }
    return 0;
  }

  int mixed(int arg0) {
    int var2 = 0;
    switch (arg0) {
      case 1:
        return 100;
      case 2:
        var2 = 2;
        break;
      default:
        var2 = 3;
    }
    return var2;
  }
}
